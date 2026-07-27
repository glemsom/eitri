// Package loop provides the agent turn loop — RunAgent drives the synchronous
// LLM request/tool-dispatch cycle until completion, cancellation, or max turns.
//
// Extracted from the runner monolith (ticket #691).
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	runtimeDebug "runtime/debug"
	"strings"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tokenizer"
	"github.com/glemsom/eitri/internal/tool"
)

// RunSpec holds the transport/config fields for RunAgent.
// These are the LLM service, request, tools, SSE writer, and turn/history caps.
type RunSpec struct {
	// Client is the litellm client used to generate streaming responses.
	Client *litellm.Client

	// Request is the base LLM request configuration (model, thinking, provider
	// options). Each turn the loop copies the base fields and sets the current
	// conversation history and tool definitions.
	Request *litellm.Request

	// MaxTurns is the maximum number of assistant turns before the loop exits.
	MaxTurns int

	// MaxHistory is the maximum number of messages to keep in conversation history.
	MaxHistory int

	// SSEWriter broadcasts SSE events through the writer.
	SSEWriter *runstate.Writer

	// Tools is the registry of tools available to the agent.
	Tools *tool.Registry
}

// TurnCompleter is the interface for post-turn completion callbacks.
// Implementations can persist session state, trigger compaction,
// broadcast SSE events, or perform other post-turn cleanup.
type TurnCompleter interface {
	OnTurnComplete(ctx context.Context, sessionID string)
}

// RunOpts bundles the runtime/UI options for RunAgent.
// Using a single struct makes the function signature stable and self-documenting.
// Use DefaultRunOpts() to obtain safe defaults.
type RunOpts struct {
	// HistoryMgr handles reading and appending conversation history.
	// Two concrete types exist: NewSessionHistoryManager (browser UI path)
	// and NewRequestHistoryManager (headless/direct-messages path).
	HistoryMgr HistoryManager

	// Confirmer handles user confirmation for path-based tool access.
	// When nil, confirmation-dependent operations return errors to the LLM.
	Confirmer Confirmer

	// UISessionMgr manages UI session state. Used for broadcasting components
	// and quick replies to browser-based sessions.
	UISessionMgr *uisession.Manager

	// SessionID identifies the conversation session.
	SessionID string

	// ContextWindow is the configured context window token limit. When > 0,
	// context_update SSE events are broadcast after each turn. When <= 0,
	// no context_update events are emitted.
	ContextWindow int

	// CrashDumpFunc is called on panic with the error and stack trace. Optional.
	CrashDumpFunc func(err error, stack []byte)

	// Turns is updated each turn with the current turn count. Optional.
	Turns *int

	// DebugLLMDir is the directory for writing LLM debug files on error.
	// When empty, no debug files are written.
	DebugLLMDir string

	// TurnCompleter, if non-nil, is called after each complete turn (assistant
	// message appended + all tool results processed). The sessionID is already
	// set on the RunOpts struct. The snapshot runs synchronously and blocks
	// the next turn or the SSE "done" event.
	TurnCompleter TurnCompleter

	// CalibrationStore tracks per-model chars-per-token averages.
	// When non-nil, token usage from LLM responses is fed back to update
	// the calibration after each streaming response completes.
	CalibrationStore *tokenizer.CalibrationStore

	// ModelName is the current model identifier, used as the key for
	// calibration lookups and updates. When empty, calibration is skipped.
	ModelName string
}

// DefaultRunOpts returns a RunOpts with safe defaults (nil callbacks).
func DefaultRunOpts() RunOpts {
	return RunOpts{}
}

// buildLitellmRequest creates a per-turn litellm.Request from the base request
// config, current conversation history, and tool definitions.
func buildLitellmRequest(base *litellm.Request, history []llm.Message, tools []litellm.Tool) *litellm.Request {
	messages := make([]litellm.Message, 0, len(history))
	for _, m := range history {
		messages = append(messages, toLitellmMessage(m))
	}

	lr := &litellm.Request{
		Model:    base.Model,
		Messages: messages,
		Tools:    tools,
	}

	// Default max_tokens (required by some providers like Anthropic)
	maxTokens := 4096
	lr.MaxTokens = &maxTokens

	// Copy thinking config from base request
	if base.Thinking != nil {
		lr.Thinking = base.Thinking
	}

	// Copy provider options from base request
	if base.ProviderOptions != nil {
		lr.ProviderOptions = base.ProviderOptions
	}

	return lr
}

// toLitellmMessage converts an llm.Message to a litellm.Message.
func toLitellmMessage(m llm.Message) litellm.Message {
	var blocks []litellm.Block

	// Assistant messages with tool calls get structured blocks
	if m.Role == "assistant" && len(m.ToolCalls) > 0 {
		if m.Content != "" {
			blocks = append(blocks, litellm.TextBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			blocks = append(blocks, litellm.ToolUseBlock{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	} else if m.Role == "tool" {
		blocks = append(blocks, litellm.ToolResultBlock{
			ToolUseID: m.ToolCallID,
			Content:   []litellm.Block{litellm.TextBlock{Text: m.Content}},
		})
	} else {
		// System, user, or simple assistant messages
		content := m.Content
		if m.ReasoningContent != "" {
			content = m.ReasoningContent + "\n" + content
		}
		blocks = append(blocks, litellm.TextBlock{Text: content})
	}

	return litellm.Message{
		Role:   litellm.Role(m.Role),
		Blocks: blocks,
	}
}

// IsReasoningModel returns true for models known to support thinking/reasoning
// via the litellm library's Thinking field. Used by callers to gate whether
// Thinking.ThinkingEnabled is set on the litellm.Request.
func IsReasoningModel(model string) bool {
	lower := strings.ToLower(model)
	if _, after, ok := strings.Cut(lower, "/"); ok {
		lower = after
	}
	return strings.HasPrefix(lower, "gpt-5")
}

// RunAgent drives the synchronous agent turn loop.
//
// It sends the request to the LLM, processes tool calls via the registry,
// and broadcasts SSE events through the writer. The loop continues until
// the LLM returns a response with no tool calls, maxTurns is reached,
// or the context is cancelled.
//
// Tool execution errors (file not found, command failed) and dispatch errors
// (unknown tool, e.g. LLM hallucinating "replace" instead of "edit") are
// fed back to the LLM as tool result content — the LLM decides how to respond.
// Only context cancellation and max turns terminate the loop.
//
// When a tool returns ToolResult with NeedsConfirm=true, the loop calls
// confirmer to pause for user input. On approval, the tool is re-executed
// with the path temporarily allowed. On denial, an error is returned to the
// LLM.
func RunAgent(ctx context.Context, spec RunSpec, opts RunOpts) error {
	maxTurns := spec.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	// Panic recovery: write crash dump then re-panic
	if opts.CrashDumpFunc != nil {
		defer func() {
			if r := recover(); r != nil {
				var err error
				switch x := r.(type) {
				case error:
					err = x
				default:
					err = fmt.Errorf("panic: %v", x)
				}
				opts.CrashDumpFunc(err, runtimeDebug.Stack())
				panic(r)
			}
		}()
	}

	// Helper to broadcast context_update if enabled and historyMgr is available.
	// actualUsage may be nil; if set, its prompt/completion tokens are included
	// in the ContextUpdate as actual provider usage for the current LLM call.
	broadcastContextUpdate := func(actualUsage *litellm.Usage) {
		if opts.ContextWindow <= 0 {
			return
		}
		if opts.HistoryMgr.RequestBased() {
			return
		}
		history := opts.HistoryMgr.History()
		if history == nil {
			return
		}
		update := runstate.ComputeContext(history, opts.ContextWindow, nil, "")
		if actualUsage != nil {
			update.ActualPromptTokens = actualUsage.InputTokens
			update.ActualCompletionTokens = actualUsage.OutputTokens
		}
		spec.SSEWriter.ContextUpdate(update)
	}

	// Compute tool definitions once before the loop, then reuse on every turn.
	// This avoids re-iterating the registry and allocating new slices per turn.
	var toolDefs []litellm.Tool
	if spec.Tools != nil {
		toolDefs = spec.Tools.LitellmTools()
	}

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Set current turn on the SSE writer so all events carry the turn number.
		spec.SSEWriter.SetTurn(turn + 1)

		// Load conversation history via adapter
		history := opts.HistoryMgr.History()

		// Build litellm request from llm request + current history + tools
		litellmReq := buildLitellmRequest(spec.Request, history, toolDefs)

		slog.Debug("llm turn", slog.Int("turn", turn), slog.Int("tools", len(litellmReq.Tools)), slog.Int("messages", len(litellmReq.Messages)))

		// Call LLM streaming with retry on transient errors
		const maxRetries = 5
		var (
			content    strings.Builder
			toolCalls  []litellm.ToolUseBlock
			usage      *litellm.Usage
			streamErr  error
		)
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stream, err := spec.Client.Stream(ctx, *litellmReq)
			if err == nil {
				// Process stream events inline
				content.Reset()
				toolCalls = nil
				usage = nil
				content, toolCalls, usage, streamErr = processStream(ctx, stream, spec.SSEWriter)
				if streamErr == nil {
					break
				}
				// Context cancellation: preserve partial result, exit retry loop
				if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
					break
				}
				// If stream processing returned an error, check if it's retryable
				if attempt < maxRetries && litellm.IsRetryableError(streamErr) {
					slog.Warn("llm stream transient error, retrying",
						slog.Int("attempt", attempt+1),
						slog.Int("max", maxRetries),
						slog.Any("error", streamErr),
					)
					dumpRequestOnError(litellmReq, streamErr, attempt+1, opts.DebugLLMDir)
					time.Sleep(1 * time.Second)
					continue
				}
				// Non-retryable stream error
				dumpRequestOnError(litellmReq, streamErr, maxRetries+1, opts.DebugLLMDir)
				msg := fmt.Sprintf("LLM error: %v", streamErr)
				spec.SSEWriter.Error(msg)
				return fmt.Errorf("chat stream: %w", streamErr)
			}

			// Error starting the stream
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				streamErr = err
				break
			}
			if attempt < maxRetries && litellm.IsRetryableError(err) {
				slog.Warn("llm chat stream transient error, retrying",
					slog.Int("attempt", attempt+1),
					slog.Int("max", maxRetries),
					slog.Any("error", err),
				)
				dumpRequestOnError(litellmReq, err, attempt+1, opts.DebugLLMDir)
				time.Sleep(1 * time.Second)
				continue
			}
			dumpRequestOnError(litellmReq, err, maxRetries+1, opts.DebugLLMDir)
			msg := fmt.Sprintf("LLM error: %v", err)
			spec.SSEWriter.Error(msg)
			return fmt.Errorf("chat stream: %w", err)
		}

		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				// Preserve partial result: append assistant message with accumulated
				// content and any tool calls to conversation history before returning.
				opts.HistoryMgr.AppendAssistant(content.String(), toolCallsToLLM(toolCalls))
				if opts.HistoryMgr.RequestBased() {
					trimMessages(spec.Request, spec.MaxHistory)
				}
				return streamErr
			}
			spec.SSEWriter.Error(runstate.FormatErrorMessage(streamErr))
			return streamErr
		}

		// Feed provider usage data into CalibrationStore.
		updateCalibration(opts.CalibrationStore, opts.ModelName, history, usage)

		if len(toolCalls) > 0 {
			slog.Debug("tool calls received", slog.Int("count", len(toolCalls)))
			for _, tc := range toolCalls {
				slog.Debug("tool call", slog.String("id", tc.ID), slog.String("tool", tc.Name), slog.String("args", string(tc.Arguments)))
			}
		}

		// No tool calls → done, append final assistant message
		if len(toolCalls) == 0 {
			contentStr := content.String()

			// Broadcast final context_update before done, including actual provider usage
			broadcastContextUpdate(usage)

			usage := runstate.EstimateUsage(contentStr, nil, "")
			spec.SSEWriter.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), usage)
			// Append final assistant response to conversation history
			if contentStr != "" || len(history) > 0 {
				opts.HistoryMgr.AppendAssistant(contentStr, nil)
			}
			// Trim conversation history if cap is set (only when not using session manager)
			if opts.HistoryMgr.RequestBased() {
				trimMessages(spec.Request, spec.MaxHistory)
			}

			// Fire per-turn snapshot callback
			if opts.TurnCompleter != nil {
				opts.TurnCompleter.OnTurnComplete(ctx, opts.SessionID)
			}
			return nil
		}

		// Trim conversation history if cap is set (only when not using session manager)
		if opts.HistoryMgr.RequestBased() {
			trimMessages(spec.Request, spec.MaxHistory)
		}

		// Has tool calls — add assistant message to history
		opts.HistoryMgr.AppendAssistant(content.String(), toolCallsToLLM(toolCalls))

		// Execute each tool call sequentially
		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}

			// Parse arguments
			var args json.RawMessage
			if len(tc.Arguments) > 0 {
				if err := json.Unmarshal(tc.Arguments, &args); err != nil {
					args = tc.Arguments
				} else {
					args = tc.Arguments
				}
			}

			// Broadcast tool call event
			argsForDisplay := args
			if len(argsForDisplay) == 0 {
				argsForDisplay = json.RawMessage("{}")
			}
			spec.SSEWriter.ToolCall(tc.Name, argsForDisplay)

			// Dispatch tool via registry
			dispResult, dispErr := spec.Tools.Dispatch(ctx, tc.ID, tc.Name, args)
			if dispErr != nil {
				errMsg := fmt.Sprintf("Tool error: %v", dispErr)
				spec.SSEWriter.ToolResult(tc.Name, errMsg)
				opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
				slog.Warn("tool dispatch error", slog.String("tool", tc.Name), slog.String("error", errMsg))
				continue
			}

			// Check if tool needs user confirmation
			if dispResult.NeedsConfirm && opts.Confirmer != nil {
				confPath := dispResult.ConfirmPath
				confMsg := dispResult.ConfirmMessage
				slog.Debug("tool needs confirmation", slog.String("path", confPath), slog.String("message", confMsg))

				spec.SSEWriter.State().Broadcast(runstate.SSEEvent{
					Type:    "needs_confirmation",
					Content: confMsg,
					Data:    map[string]any{"path": confPath, "message": confMsg},
				})

				confirmResult, confirmErr := opts.Confirmer.Confirm(ctx, opts.SessionID, confPath, confMsg)
				if confirmErr != nil {
					if errors.Is(confirmErr, context.Canceled) || errors.Is(confirmErr, context.DeadlineExceeded) {
						return confirmErr
					}
					errMsg := fmt.Sprintf("Confirmation error: %v", confirmErr)
					spec.SSEWriter.ToolResult(tc.Name, errMsg)
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
					continue
				}

				if confirmResult.Approved {
					addReadToolAllowedPath(spec.Tools, confPath)
					dispResult, dispErr = spec.Tools.Dispatch(ctx, tc.ID, tc.Name, args)
					if dispErr != nil {
						errMsg := fmt.Sprintf("Tool error after approval: %v", dispErr)
						spec.SSEWriter.ToolResult(tc.Name, errMsg)
						opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
						continue
					}
				} else {
					errMsg := "Access denied to path: " + confPath
					spec.SSEWriter.ToolResult(tc.Name, errMsg)
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
					continue
				}
			}

			// Extract result text from blocks
			blocks := dispResult.Blocks
			resultText := blocksToText(blocks)
			isError := toolResultHasError(blocks)
			slog.Debug("tool result", slog.String("tool", tc.Name), slog.String("result", TruncateText(resultText, 200)), slog.Bool("error", isError))

			// Broadcast tool result event
			spec.SSEWriter.ToolResult(tc.Name, resultText)

			// Broadcast skill_activated if this was a successful skill load
			if tc.Name == "skill" && !isError {
				var skillArgs struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(args, &skillArgs); err == nil && skillArgs.Name != "" {
					spec.SSEWriter.SkillActivated(skillArgs.Name)
				}
			}

			// Emit component event for compatible tools (except QuickReplies which stores inline)
			if !isError || tc.Name == "render_quick_replies" {
				compName, compData, ok := emitComponentForTool(spec.SSEWriter, tc.Name, args, blocks)
				if ok && opts.UISessionMgr != nil {
					if tc.Name == "render_quick_replies" {
						if rawOpts, ok := compData["options"]; ok {
							if optStrs, ok := rawOpts.([]string); ok {
								_ = opts.UISessionMgr.SetQuickReplies(opts.SessionID, optStrs)
							}
						}
					} else {
						_ = opts.UISessionMgr.AppendComponent(opts.SessionID, llm.ComponentData{
							Name: compName,
							Data: compData,
						})
					}
				}
			}

			// Add tool result message to conversation history
			resultContent := resultText
			if isError && resultContent == "" {
				resultContent = fmt.Sprintf("Error executing %q", tc.Name)
			}
			opts.HistoryMgr.AppendTool(tc.ID, resultContent, isError)
		}

		// Broadcast context_update after tool results appended to history
		broadcastContextUpdate(usage)

		// Update turn count for external consumers
		if opts.Turns != nil {
			*opts.Turns = turn + 1
		}

		// Fire per-turn snapshot callback
		if opts.TurnCompleter != nil {
			opts.TurnCompleter.OnTurnComplete(ctx, opts.SessionID)
		}
	}

	// Max turns exceeded
	broadcastContextUpdate(nil)
	msg := runstate.MaxTurnsMessage(maxTurns)
	spec.SSEWriter.Error(msg)
	return &MaxTurnsExceededError{Limit: maxTurns}
}

// processStream consumes a litellm.Stream using Next() and a type-switch,
// accumulating content, tool calls, and usage until DoneEvent or ErrorEvent.
func processStream(
	ctx context.Context,
	stream litellm.Stream,
	sseWriter *runstate.Writer,
) (strings.Builder, []litellm.ToolUseBlock, *litellm.Usage, error) {
	defer stream.Close()

	// Close the stream when the context is cancelled so Next() unblocks.
	go func() {
		<-ctx.Done()
		stream.Close()
	}()

	var content strings.Builder
	var toolCalls []litellm.ToolUseBlock
	var usage *litellm.Usage
	toolAcc := litellm.NewToolUseAccumulator()

	for {
		// Check context before each event
		if err := ctx.Err(); err != nil {
			// Drain any remaining buffered events before returning cancellation
			drainRemaining(stream, &content, &toolCalls, &usage, toolAcc, sseWriter)
			return content, toolCalls, usage, err
		}

		event, err := stream.Next()
		if err != nil {
			// If the context was cancelled, return context error instead of stream error
			if errors.Is(err, io.EOF) {
				if cerr := ctx.Err(); cerr != nil {
					return content, toolCalls, usage, cerr
				}
				// Premature EOF — stream ended without DoneEvent
				return content, toolCalls, usage, fmt.Errorf("premature EOF: %w", io.ErrUnexpectedEOF)
			}
			return content, toolCalls, usage, err
		}

		switch e := event.(type) {
		case litellm.ContentDelta:
			content.WriteString(e.Text)
			sseWriter.Token(e.Text)

		case litellm.ReasoningDelta:
			sseWriter.ThinkingDelta(e.Text)

		case litellm.ToolUseStart:
			toolAcc.Start(e)

		case litellm.ToolUseDelta:
			toolAcc.Delta(e)

		case litellm.ToolUseDone:
			_, block, _ := toolAcc.Done(e)
			if block != nil {
				toolCalls = append(toolCalls, *block)
			}

		case litellm.UsageEvent:
			usage = &e.Usage

		case litellm.DoneEvent:
			// Flush any remaining tool calls from the accumulator
			return content, toolCalls, usage, nil

		case litellm.ErrorEvent:
			return content, toolCalls, usage, e.Err
		}
	}
}

// drainRemaining reads any buffered events from the stream when the context
// has been cancelled. This mirrors the previous drainStream buffered-event
// drain behaviour.
func drainRemaining(
	stream litellm.Stream,
	content *strings.Builder,
	toolCalls *[]litellm.ToolUseBlock,
	usage **litellm.Usage,
	toolAcc *litellm.ToolUseAccumulator,
	sseWriter *runstate.Writer,
) {
	for {
		event, err := stream.Next()
		if err != nil {
			return
		}
		switch e := event.(type) {
		case litellm.ContentDelta:
			content.WriteString(e.Text)
			sseWriter.Token(e.Text)
		case litellm.ReasoningDelta:
			sseWriter.ThinkingDelta(e.Text)
		case litellm.ToolUseStart:
			toolAcc.Start(e)
		case litellm.ToolUseDelta:
			toolAcc.Delta(e)
		case litellm.ToolUseDone:
			_, block, _ := toolAcc.Done(e)
			if block != nil {
				*toolCalls = append(*toolCalls, *block)
			}
		case litellm.UsageEvent:
			*usage = &e.Usage
		case litellm.DoneEvent:
			return
		case litellm.ErrorEvent:
			return
		}
	}
}

// toolCallsToLLM converts []litellm.ToolUseBlock to []llm.ToolCall for
// storage in HistoryManager.
func toolCallsToLLM(tcs []litellm.ToolUseBlock) []llm.ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, len(tcs))
	for i, tc := range tcs {
		args := ""
		if len(tc.Arguments) > 0 {
			args = string(tc.Arguments)
		}
		out[i] = llm.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: llm.FunctionCall{
				Name:      tc.Name,
				Arguments: args,
			},
		}
	}
	return out
}

// MaxTurnsExceededError reports that a run hit its configured turn cap.
type MaxTurnsExceededError struct {
	Limit int
}

func (e *MaxTurnsExceededError) Error() string {
	return fmt.Sprintf("max turns limit reached: %d", e.Limit)
}

// updateCalibration feeds provider usage data from a completed LLM response
// into the CalibrationStore. It computes chars_per_token = inputLen / promptTokens
// and calls store.Update(model, cpt).
func updateCalibration(store *tokenizer.CalibrationStore, model string, messages []llm.Message, usage *litellm.Usage) {
	if store == nil || model == "" || usage == nil || usage.InputTokens <= 0 {
		return
	}

	// Compute total input text length from the messages sent in the request.
	inputLen := 0
	for _, msg := range messages {
		inputLen += len(msg.Content)
	}
	if inputLen == 0 {
		return
	}

	cpt := float64(inputLen) / float64(usage.InputTokens)

	oldCPT := store.Lookup(model)
	store.Update(model, cpt)
	newCPT := store.Lookup(model)

	slog.Debug("calibration update",
		slog.String("model", model),
		slog.Float64("old_cpt", oldCPT),
		slog.Float64("new_cpt", newCPT),
		slog.Float64("observed_cpt", cpt),
		slog.Int("prompt_tokens", usage.InputTokens),
		slog.Int("input_chars", inputLen),
	)
}
