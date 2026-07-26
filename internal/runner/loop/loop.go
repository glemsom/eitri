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
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runner/adapters"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tokenizer"
	"github.com/glemsom/eitri/internal/tool"
)

// RunSpec holds the transport/config fields for RunAgent.
// These are the LLM service, request, tools, SSE writer, and turn/history caps.
type RunSpec struct {
	// Service is the LLM service used to generate responses.
	Service llm.LLMService

	// Request is the LLM request to send.
	Request *llm.Request

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
	HistoryMgr adapters.HistoryManager

	// Confirmer handles user confirmation for path-based tool access.
	// When nil, confirmation-dependent operations return errors to the LLM.
	Confirmer adapters.Confirmer

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
	broadcastContextUpdate := func() {
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
		spec.SSEWriter.ContextUpdate(update)
	}

	spec.Request.Stream = true

	// Compute tool definitions once before the loop, then reuse on every turn.
	// This avoids re-iterating the registry and allocating new slices per turn.
	var toolDefs []llm.ToolDef
	if spec.Tools != nil {
		toolDefs = toolDefsFromRegistry(spec.Tools)
	}

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Set current turn on the SSE writer so all events carry the turn number.
		spec.SSEWriter.SetTurn(turn + 1)

		// Load conversation history via adapter
		spec.Request.Messages = opts.HistoryMgr.History()

		// Attach tool definitions (computed once before the loop)
		if toolDefs != nil {
			spec.Request.Tools = toolDefs
		}

		slog.Debug("llm turn", slog.Int("turn", turn), slog.Int("tools", len(spec.Request.Tools)), slog.Int("messages", len(spec.Request.Messages)))

		// Call LLM streaming with retry on transient errors
		const maxRetries = 5
		var (
			stream <-chan llm.StreamEvent
			err    error
		)
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stream, err = spec.Service.ChatStream(ctx, *spec.Request)
			if err == nil {
				break
			}
			if attempt < maxRetries && isRetryableLLMError(err) {
				slog.Warn("llm chat stream transient error, retrying",
					slog.Int("attempt", attempt+1),
					slog.Int("max", maxRetries),
					slog.Any("error", err),
				)
				dumpRequestOnError(spec.Request, err, attempt+1, opts.DebugLLMDir)
				time.Sleep(1 * time.Second)
				continue
			}
			dumpRequestOnError(spec.Request, err, maxRetries+1, opts.DebugLLMDir)
			msg := fmt.Sprintf("LLM error: %v", err)
			spec.SSEWriter.Error(msg)
			return fmt.Errorf("chat stream: %w", err)
		}

		// Process stream events
		content, toolCalls, usage, streamErr := drainStream(ctx, stream, spec.SSEWriter)
		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				// Preserve partial result: append assistant message with accumulated
				// content and any tool calls to conversation history before returning.
				// Always save even when empty (e.g. thinking-only stream) to maintain
				// user→assistant→user alternation — otherwise next user message creates
				// consecutive user messages which some providers reject as malformed.
				opts.HistoryMgr.AppendAssistant(content.String(), toolCalls)
				if opts.HistoryMgr.RequestBased() {
					trimMessages(spec.Request, spec.MaxHistory)
				}
				return streamErr
			}
			spec.SSEWriter.Error(runstate.FormatErrorMessage(streamErr))
			return streamErr
		}

		// Feed provider usage data into CalibrationStore.
		updateCalibration(opts.CalibrationStore, opts.ModelName, spec.Request.Messages, usage)

		if len(toolCalls) > 0 {
			slog.Debug("tool calls received", slog.Int("count", len(toolCalls)))
			for _, tc := range toolCalls {
				slog.Debug("tool call", slog.String("id", tc.ID), slog.String("tool", tc.Function.Name), slog.String("args", tc.Function.Arguments))
			}
		}

		// No tool calls → done, append final assistant message
		if len(toolCalls) == 0 {
			contentStr := content.String()

			// Broadcast final context_update before done
			broadcastContextUpdate()

			usage := runstate.EstimateUsage(contentStr, nil, "")
			spec.SSEWriter.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), usage)
			// Append final assistant response to conversation history
			if contentStr != "" || len(spec.Request.Messages) > 0 {
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
		opts.HistoryMgr.AppendAssistant(content.String(), toolCalls)

		// Execute each tool call sequentially

		for _, tc := range toolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}

			// Parse arguments
			var args json.RawMessage
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = json.RawMessage(tc.Function.Arguments)
			}

			// Broadcast tool call event
			// Sanitize args: json.RawMessage with empty content breaks marshaling.
			// LLMs sometimes produce empty arguments (e.g. for hallucinated tools).
			argsForDisplay := args
			if len(argsForDisplay) == 0 {
				argsForDisplay = json.RawMessage("{}")
			}
			spec.SSEWriter.ToolCall(tc.Function.Name, argsForDisplay)

			// Dispatch tool via registry
			dispResult, dispErr := spec.Tools.Dispatch(ctx, tc.ID, tc.Function.Name, args)
			if dispErr != nil {
				// Feed unknown tool / dispatch errors back to the LLM as tool
				// result instead of terminating the loop. LLMs commonly hallucinate
				// tool names (e.g. "replace" instead of "edit") — this gives them
				// a chance to self-correct on the next turn.
				errMsg := fmt.Sprintf("Tool error: %v", dispErr)
				// Broadcast tool result so the error shows in the tool card
				// (not as a separate error toast that closes the stream).
				spec.SSEWriter.ToolResult(tc.Function.Name, errMsg)
				// Record the error as a tool result so the LLM can see it
				opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
				slog.Warn("tool dispatch error", slog.String("tool", tc.Function.Name), slog.String("error", errMsg))
				continue
			}

			// Check if tool needs user confirmation
			if dispResult.NeedsConfirm && opts.Confirmer != nil {
				confPath := dispResult.ConfirmPath
				confMsg := dispResult.ConfirmMessage
				slog.Debug("tool needs confirmation", slog.String("path", confPath), slog.String("message", confMsg))

				// Send needs_confirmation SSE event
				spec.SSEWriter.State().Broadcast(runstate.SSEEvent{
					Type:    "needs_confirmation",
					Content: confMsg,
					Data:    map[string]any{"path": confPath, "message": confMsg},
				})

				// Wait for user response
				confirmResult, confirmErr := opts.Confirmer.Confirm(ctx, opts.SessionID, confPath, confMsg)
				if confirmErr != nil {
					if errors.Is(confirmErr, context.Canceled) || errors.Is(confirmErr, context.DeadlineExceeded) {
						return confirmErr
					}
					errMsg := fmt.Sprintf("Confirmation error: %v", confirmErr)
					spec.SSEWriter.ToolResult(tc.Function.Name, errMsg)
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
					continue
				}

				if confirmResult.Approved {
					// Temporarily add the path to ReadTool's allowedPaths
					// and re-dispatch
					addReadToolAllowedPath(spec.Tools, confPath)
					dispResult, dispErr = spec.Tools.Dispatch(ctx, tc.ID, tc.Function.Name, args)
					if dispErr != nil {
						errMsg := fmt.Sprintf("Tool error after approval: %v", dispErr)
						spec.SSEWriter.ToolResult(tc.Function.Name, errMsg)
						opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
						continue
					}
					// Continue to process blocks below (resultText, Broadcast, etc.)
				} else {
					// Denial — return error to LLM
					errMsg := "Access denied to path: " + confPath
					spec.SSEWriter.ToolResult(tc.Function.Name, errMsg)
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, true)
					continue
				}
			}

			// Extract result text from blocks
			blocks := dispResult.Blocks
			resultText := blocksToText(blocks)
			isError := toolResultHasError(blocks)
			slog.Debug("tool result", slog.String("tool", tc.Function.Name), slog.String("result", TruncateText(resultText, 200)), slog.Bool("error", isError))

			// Broadcast tool result event
			spec.SSEWriter.ToolResult(tc.Function.Name, resultText)

			// Broadcast skill_activated if this was a successful skill load
			if tc.Function.Name == "skill" && !isError {
				// Extract the skill name from the tool call arguments
				var skillArgs struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(args, &skillArgs); err == nil && skillArgs.Name != "" {
					spec.SSEWriter.SkillActivated(skillArgs.Name)
				}
			}

			// Emit component event for compatible tools (except QuickReplies which stores inline)
			if !isError || tc.Function.Name == "render_quick_replies" {
				compName, compData, ok := emitComponentForTool(spec.SSEWriter, tc.Function.Name, args, blocks)
				if ok && opts.UISessionMgr != nil {
					if tc.Function.Name == "render_quick_replies" {
						// QuickReplies stores inline on the assistant message, not as a component event
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
				resultContent = fmt.Sprintf("Error executing %q", tc.Function.Name)
			}
			opts.HistoryMgr.AppendTool(tc.ID, resultContent, isError)
		}

		// Broadcast context_update after tool results appended to history
		broadcastContextUpdate()

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
	// Broadcast final context_update before error
	broadcastContextUpdate()
	msg := runstate.MaxTurnsMessage(maxTurns)
	spec.SSEWriter.Error(msg)
	return &MaxTurnsExceededError{Limit: maxTurns}
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
//
// Edge cases handled:
//   - store is nil → skip
//   - model name is empty → skip
//   - usage is nil or PromptTokens == 0 → skip
//   - first turn uses default CPT if no calibration data exists yet
//
// Calibration changes are logged at Debug level.
func updateCalibration(store *tokenizer.CalibrationStore, model string, messages []llm.Message, usage *llm.Usage) {
	if store == nil || model == "" || usage == nil || usage.PromptTokens <= 0 {
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

	cpt := float64(inputLen) / float64(usage.PromptTokens)

	oldCPT := store.Lookup(model)
	store.Update(model, cpt)
	newCPT := store.Lookup(model)

	slog.Debug("calibration update",
		slog.String("model", model),
		slog.Float64("old_cpt", oldCPT),
		slog.Float64("new_cpt", newCPT),
		slog.Float64("observed_cpt", cpt),
		slog.Int("prompt_tokens", usage.PromptTokens),
		slog.Int("input_chars", inputLen),
	)
}
