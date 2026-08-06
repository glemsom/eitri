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
	"maps"
	runtimeDebug "runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tokenizer"
	"github.com/glemsom/eitri/internal/tool"
	"github.com/glemsom/eitri/internal/uixt"
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

	// RunID identifies the run this loop belongs to. It is stamped onto every
	// HTTP trace (via TraceMeta) so traces can be correlated back to their
	// turn by (RunID, Turn) in session reports (issue #988). When empty, traces
	// record no run correlation.
	RunID string

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

	// TurnTimeout caps the wall-clock duration of a single LLM turn (streaming
	// request + response). It guards against a provider streaming reasoning
	// forever without emitting content or a tool call. When <= 0 the turn is
	// not bounded. Defaults to 5 minutes.
	TurnTimeout time.Duration

	// RetryPolicy controls retries of transient LLM errors during the run
	// loop. When nil, DefaultRetryPolicy() is used (5 retries, 1s backoff).
	// Inject a zero-attempt policy in tests so dead endpoints fail fast
	// instead of sleeping 5×1s.
	RetryPolicy *RetryPolicy
}

// RetryPolicy configures how the run loop retries transient LLM errors.
// Attempts is the number of retries after the initial attempt (0 disables
// retries); Backoff is the sleep between attempts (0 disables the sleep).
// The zero value is a valid policy: a single attempt, no backoff.
type RetryPolicy struct {
	Attempts int
	Backoff  time.Duration
}

// DefaultRetryPolicy returns the production retry policy: 5 retries with 1s
// backoff — the historical hardcoded behavior.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Attempts: 5, Backoff: time.Second}
}

// DefaultRunOpts returns a RunOpts with safe defaults (nil callbacks).
func DefaultRunOpts() RunOpts {
	return RunOpts{TurnTimeout: 5 * time.Minute}
}

// buildLitellmRequest creates a per-turn litellm.Request from the base request
// config, current conversation history, and tool definitions.
func buildLitellmRequest(base *litellm.Request, history []message.EitriMessage, tools []litellm.Tool) *litellm.Request {
	messages := make([]litellm.Message, 0, len(history))
	for _, m := range history {
		messages = append(messages, m.ToLitellm())
	}

	lr := &litellm.Request{
		Model:    base.Model,
		Messages: messages,
		Tools:    tools,
	}

	// Max output tokens per assistant turn. Some providers (Anthropic) require
	// the field. Fall back to a generous 32000 if the base request did not set
	// one (configurable via config.MaxOutputTokens); a small cap lets reasoning
	// models exhaust their budget on thinking and never emit a tool call.
	maxTokens := 32000
	if base.MaxTokens != nil && *base.MaxTokens > 0 {
		maxTokens = *base.MaxTokens
	}
	lr.MaxTokens = &maxTokens

	// Copy thinking config from base request
	if base.Thinking != nil {
		lr.Thinking = base.Thinking
	}

	// Copy provider options from base request (defensive copy to avoid
	// shared-mutation issues if the map is ever modified downstream).
	if base.ProviderOptions != nil {
		lr.ProviderOptions = make(litellm.ProviderOptions, len(base.ProviderOptions))
		maps.Copy(lr.ProviderOptions, base.ProviderOptions)
	}

	return lr
}

// toLitellmMessage converts an EitriMessage to a litellm.Message by calling .ToLitellm().
func toLitellmMessage(m message.EitriMessage) litellm.Message {
	return m.ToLitellm()
}

// litellmMsgText returns the concatenated text from all TextBlocks in a
// litellm.Message, including text nested inside ToolResultBlocks. This mirrors
// what the provider tokenizes when counting prompt tokens: tool results are
// part of the payload. Excluding them made the measured chars-per-token ratio
// collapse (a 90KB tool result measured as ~0 chars), poisoning the
// CalibrationStore and inflating every later context estimate.
func litellmMsgText(msg litellm.Message) string {
	var text strings.Builder
	for _, block := range msg.Blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			text.WriteString(b.Text)
		case litellm.ToolResultBlock:
			for _, c := range b.Content {
				if tb, ok := c.(litellm.TextBlock); ok {
					text.WriteString(tb.Text)
				}
			}
		}
	}
	return text.String()
}

// toFlatMessages converts []message.EitriMessage to []message.Message for
// consumers that need the flat field access pattern (e.g. ComputeContext).
func toFlatMessages(msgs []message.EitriMessage) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m.ToMessage()
	}
	return out
}

// IsReasoningModel returns true for models known to support thinking/reasoning
// control via the litellm library's Thinking field. Used by callers to gate
// whether Thinking.ThinkingEnabled is set on the litellm.Request.
//
// DELIBERATELY keep this in sync with the underlying litellm openai provider's
// own isReasoningModel (gpt-5*). Sending Thinking for a model litellm's openai
// provider doesn't classify as a reasoning chat model (e.g. deepseek-reasoner)
// is rejected with "thinking is only supported for reasoning chat models".
// DeepSeek reasoning is a server-side default and NOT a client-side budget
// knob — cap it with RunOpts.TurnTimeout instead.
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

	retryPolicy := DefaultRetryPolicy()
	if opts.RetryPolicy != nil {
		retryPolicy = *opts.RetryPolicy
	}
	maxRetries := retryPolicy.Attempts

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
		update := tokenizer.ComputeContext(toFlatMessages(history), opts.ContextWindow, opts.CalibrationStore, opts.ModelName)
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
		var (
			content      strings.Builder
			toolCalls    []litellm.ToolUseBlock
			usage        *litellm.Usage
			finishReason litellm.FinishReason
			streamErr    error
			attemptsMade int
			callStart    time.Time
		)
		// TraceMeta bridges the per-call measurements parsed from the stream
		// (usage, finish_reason, model) and the retry attempt number into the
		// HTTP trace recorded by the transport. It is reused across retries of
		// this turn; the attempt number is updated before each attempt. The run
		// ID and 1-based turn number are stamped so every trace records the
		// turn it belongs to (issue #988).
		traceMeta := &debug.TraceMeta{}
		traceMeta.SetRunID(opts.RunID)
		traceMeta.SetTurn(turn + 1)
		for attempt := 0; attempt <= maxRetries; attempt++ {
			attemptsMade++
			// Bound each attempt with the per-turn timeout. This caps a stalled
			// turn (e.g. a provider streaming reasoning/thinking tokens forever
			// without yielding content or a tool call). A fresh deadline per
			// attempt keeps the whole turn bounded even across retries.
			turnCtx := ctx
			var turnCancel context.CancelFunc
			if opts.TurnTimeout > 0 {
				turnCtx, turnCancel = context.WithTimeout(ctx, opts.TurnTimeout)
			}
			// Stamp the zero-based retry attempt onto the request context so the
			// trace recorder can count retries per provider/model (issue #987),
			// and onto the TraceMeta bridge which carries it (plus usage, finish
			// reason, model, TTFB) into the trace for streaming calls (issue #986).
			attemptCtx := debug.WithAttempt(turnCtx, attempt)
			traceMeta.SetAttempt(attempt)
			// Clear the first-token anchor from any previous attempt so each
			// attempt's trace records its own time-to-first-token (issue #988).
			traceMeta.ResetFirstToken()
			streamCtx := debug.WithTraceMeta(attemptCtx, traceMeta)
			// callStart anchors the LLM-call duration carried on the llm_call
			// SSE event (stream establishment + streaming). The HTTP trace
			// records the authoritative duration at finalize time.
			callStart = time.Now()
			stream, err := spec.Client.Stream(streamCtx, *litellmReq)
			if err == nil {
				// Process stream events inline
				content.Reset()
				toolCalls = nil
				usage = nil
				content, toolCalls, usage, finishReason, streamErr = processStream(turnCtx, stream, spec.SSEWriter, traceMeta)
				if turnCancel != nil {
					turnCancel()
				}
				if streamErr == nil {
					// A "length" finish means the model hit the max output token
					// cap mid-generation; the turn was truncated by the provider,
					// not by the model choosing to stop. Retrying a truncated
					// reasoning turn is futile (the budget is already spent on
					// thinking), so surface it as a non-retryable error instead
					// of silently swallowing it as a clean completion.
					if finishReason == litellm.FinishReasonLength {
						limit := 32000
						if litellmReq.MaxTokens != nil && *litellmReq.MaxTokens > 0 {
							limit = *litellmReq.MaxTokens
						}
						streamErr = &MaxOutputTokensExceededError{Limit: limit}
					}
					break
				}
				// Turn timeout: the stream exceeded the per-turn deadline (e.g. the
				// provider streamed reasoning for the whole window without yielding
				// content or a tool call). Surface it loudly and do not retry — a
				// retry would just burn another timeout window on the same stalled
				// reasoning. Mirrors the FinishReasonLength handling above.
				if opts.TurnTimeout > 0 && ctx.Err() == nil && errors.Is(streamErr, context.DeadlineExceeded) {
					streamErr = &TurnTimeoutError{Timeout: opts.TurnTimeout}
					spec.SSEWriter.Error(uixt.FormatErrorMessage(streamErr))
					dumpRequestOnError(litellmReq, streamErr, maxRetries+1, opts.DebugLLMDir)
					return fmt.Errorf("chat stream: %w", streamErr)
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
					if retryPolicy.Backoff > 0 {
						time.Sleep(retryPolicy.Backoff)
					}
					continue
				}
				// Non-retryable stream error
				dumpRequestOnError(litellmReq, streamErr, maxRetries+1, opts.DebugLLMDir)
				msg := uixt.FormatErrorMessage(streamErr)
				spec.SSEWriter.Error(msg)
				return fmt.Errorf("chat stream: %w", streamErr)
			}

			if turnCancel != nil {
				turnCancel()
			}
			// Error starting the stream — treat a turn-timeout here identically
			// to the in-stream case (non-retryable stall), unless the parent run
			// context was also cancelled.
			if opts.TurnTimeout > 0 && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				streamErr = &TurnTimeoutError{Timeout: opts.TurnTimeout}
				spec.SSEWriter.Error(uixt.FormatErrorMessage(streamErr))
				dumpRequestOnError(litellmReq, streamErr, maxRetries+1, opts.DebugLLMDir)
				return fmt.Errorf("chat stream: %w", streamErr)
			}
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
				if retryPolicy.Backoff > 0 {
					time.Sleep(retryPolicy.Backoff)
				}
				continue
			}
			dumpRequestOnError(litellmReq, err, maxRetries+1, opts.DebugLLMDir)
			msg := uixt.FormatErrorMessage(err)
			spec.SSEWriter.Error(msg)
			return fmt.Errorf("chat stream: %w", err)
		}

		toolCalls = normalizeToolCalls(toolCalls)

		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				// Preserve partial result: append assistant message with accumulated
				// content and any tool calls to conversation history before returning.
				opts.HistoryMgr.AppendAssistant(content.String(), toolCalls)
				if opts.HistoryMgr.RequestBased() {
					trimMessages(spec.Request, spec.MaxHistory)
				}
				return streamErr
			}
			var maxOutErr *MaxOutputTokensExceededError
			if errors.As(streamErr, &maxOutErr) {
				// The model exhausted its output-token budget before emitting a
				// tool call or final answer (reasoning burn). Preserve whatever
				// partial content arrived, then surface the truncation loudly so
				// it is never recorded as a silent clean completion.
				if content.Len() > 0 {
					opts.HistoryMgr.AppendAssistant(content.String(), nil)
					if opts.HistoryMgr.RequestBased() {
						trimMessages(spec.Request, spec.MaxHistory)
					}
				}
				spec.SSEWriter.Error(uixt.FormatErrorMessage(streamErr))
				return streamErr
			}
			spec.SSEWriter.Error(uixt.FormatErrorMessage(streamErr))
			return streamErr
		}

		// Emit the llm_call correlation event for this turn: it carries the
		// HTTP trace ID recorded for the successful attempt plus the retry
		// count and timing so the persisted timeline can join the turn to its
		// traces by ID (issue #988). Skipped when no recording transport is
		// attached (no trace was recorded for the call).
		if traceID := string(traceMeta.TraceID()); traceID != "" {
			spec.SSEWriter.LLMCall(runstate.LLMCallInfo{
				TraceID:    traceID,
				Attempt:    traceMeta.Attempt(),
				Attempts:   attemptsMade,
				DurationMs: time.Since(callStart).Milliseconds(),
				TTFBMs:     traceMeta.TTFBMs(),
				TTFTMs:     traceMeta.TTFTMs(),
			})
		}

		// Feed provider usage data into CalibrationStore.
		updateCalibration(opts.CalibrationStore, opts.ModelName, litellmReq.Messages, usage)

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

			// The done event carries the provider-reported usage when the
			// provider returned any; the text-length estimate is used only as a
			// fallback so the actual value is never shadowed.
			spec.SSEWriter.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), usageForDone(usage, contentStr, opts.CalibrationStore, opts.ModelName))
			// Append final assistant response to conversation history
			// Only append when there's actual content — empty assistant messages
			// produce invalid OpenAI-format JSON and may be rejected by providers.
			if contentStr != "" {
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
			args := tc.Arguments

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
				opts.HistoryMgr.AppendTool(tc.ID, errMsg, "", true)
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
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, "", true)
					continue
				}

				if confirmResult.Approved {
					addReadToolAllowedPath(spec.Tools, confPath)
					dispResult, dispErr = spec.Tools.Dispatch(ctx, tc.ID, tc.Name, args)
					if dispErr != nil {
						errMsg := fmt.Sprintf("Tool error after approval: %v", dispErr)
						spec.SSEWriter.ToolResult(tc.Name, errMsg)
						opts.HistoryMgr.AppendTool(tc.ID, errMsg, "", true)
						continue
					}
				} else {
					errMsg := "Access denied to path: " + confPath
					spec.SSEWriter.ToolResult(tc.Name, errMsg)
					opts.HistoryMgr.AppendTool(tc.ID, errMsg, "", true)
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
						_ = opts.UISessionMgr.AppendComponent(opts.SessionID, message.ComponentData{
							Name: compName,
							Data: compData,
						})
					}
				}
			}

			// Emit Screenshot component for successful browser screenshot results
			if !isError {
				compName, compData, ok := emitScreenshotComponent(spec.SSEWriter, tc.Name, blocks, opts.SessionID)
				if ok && opts.UISessionMgr != nil {
					_ = opts.UISessionMgr.AppendComponent(opts.SessionID, message.ComponentData{
						Name: compName,
						Data: compData,
					})
				}
			}

			// Add tool result message to conversation history
			resultContent := resultText
			if isError && resultContent == "" {
				resultContent = fmt.Sprintf("Error executing %q", tc.Name)
			}
			resultRawContent := blocksToText(dispResult.RawBlocks)
			opts.HistoryMgr.AppendTool(tc.ID, resultContent, resultRawContent, isError)
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
	msg := uixt.MaxTurnsMessage(maxTurns)
	spec.SSEWriter.Error(msg)
	return &MaxTurnsExceededError{Limit: maxTurns}
}

// processStream consumes a litellm.Stream using Next() and a type-switch,
// accumulating content, tool calls, and usage until DoneEvent or ErrorEvent.
//
// When traceMeta is non-nil, the provider-parsed usage, finish reason, and
// model are recorded on it as events arrive. The meta is read by the recording
// round-tripper when the stream closes, so it must be populated before this
// function returns (its deferred stream.Close() is what finalizes the trace).
//
// IMPORTANT: The OpenAI-style SSE stream provider does NOT emit ToolUseDone
// events — it emits ToolUseStart + ToolUseDelta deltas and then a final
// DoneEvent. When DoneEvent arrives, any tool calls that were started via
// ToolUseStart/Delta must be finalized from the accumulator. We track tool
// start indices locally so we can synthesize ToolUseDone events on DoneEvent.
func processStream(
	ctx context.Context,
	stream litellm.Stream,
	sseWriter *runstate.Writer,
	traceMeta *debug.TraceMeta,
) (strings.Builder, []litellm.ToolUseBlock, *litellm.Usage, litellm.FinishReason, error) {
	var closeOnce sync.Once
	closeStream := func() {
		closeOnce.Do(func() { _ = stream.Close() })
	}

	done := make(chan struct{})
	defer func() {
		close(done)
		closeStream()
	}()

	// Close the stream when the context is cancelled so Next() unblocks.
	// Stop the helper when processStream returns normally; successful runs do
	// not cancel the parent run context.
	go func() {
		select {
		case <-ctx.Done():
			closeStream()
		case <-done:
		}
	}()

	var content strings.Builder
	var toolCalls []litellm.ToolUseBlock
	var usage *litellm.Usage
	toolAcc := litellm.NewToolUseAccumulator()

	// Track which tool use IDs were started via ToolUseStart but not yet
	// finalized via ToolUseDone. Some providers (OpenAI-style SSE) never
	// emit ToolUseDone; we finalize them on DoneEvent instead.
	startedToolIDs := make(map[string]bool)

	for {
		// Check context before each event
		if err := ctx.Err(); err != nil {
			// Drain any remaining buffered events before returning cancellation
			drainRemaining(stream, &content, &toolCalls, &usage, toolAcc, sseWriter, traceMeta)
			return content, toolCalls, usage, litellm.FinishReason(""), err
		}

		event, err := stream.Next()
		if err != nil {
			// If the context was cancelled, return context error instead of stream error
			if errors.Is(err, io.EOF) {
				if cerr := ctx.Err(); cerr != nil {
					return content, toolCalls, usage, litellm.FinishReason(""), cerr
				}
				// Premature EOF — stream ended without DoneEvent
				return content, toolCalls, usage, litellm.FinishReason(""), fmt.Errorf("premature EOF: %w", io.ErrUnexpectedEOF)
			}
			return content, toolCalls, usage, litellm.FinishReason(""), err
		}

		switch e := event.(type) {
		case litellm.ContentDelta:
			// The first content token anchors time-to-first-token (issue
			// #988). TraceMeta keeps only the first recorded time per attempt
			// (the loop resets it before each attempt).
			if traceMeta != nil {
				traceMeta.SetFirstTokenTime(time.Now())
			}
			content.WriteString(e.Text)
			sseWriter.Token(e.Text)

		case litellm.ReasoningDelta:
			sseWriter.ThinkingDelta(e.Text)

		case litellm.ToolUseStart:
			// Track the tool ID so we can finalize it on DoneEvent
			// if the provider never emits ToolUseDone.
			if e.ID != "" {
				startedToolIDs[e.ID] = true
			}
			toolAcc.Start(e)

		case litellm.ToolUseDelta:
			toolAcc.Delta(e)

		case litellm.ToolUseDone:
			_, block, _ := toolAcc.Done(e)
			if block != nil {
				toolCalls = append(toolCalls, *block)
			}
			delete(startedToolIDs, e.ID)

		case litellm.UsageEvent:
			usage = &e.Usage
			if traceMeta != nil {
				traceMeta.SetUsage(debugUsageFromLitellm(usage))
				if e.Usage.Model != "" {
					traceMeta.SetModel(e.Usage.Model)
				}
			}

		case litellm.DoneEvent:
			// OpenAI-style SSE providers (including OpenCode Go) don't emit
			// ToolUseDone. Finalize any pending tool calls here.
			for id := range startedToolIDs {
				_, block, _ := toolAcc.Done(litellm.ToolUseDone{ID: id})
				if block != nil {
					toolCalls = append(toolCalls, *block)
				}
			}
			startedToolIDs = nil
			if traceMeta != nil {
				traceMeta.SetFinishReason(string(e.FinishReason))
				if e.Model != "" {
					traceMeta.SetModel(e.Model)
				}
			}
			return content, toolCalls, usage, e.FinishReason, nil

		case litellm.ErrorEvent:
			return content, toolCalls, usage, litellm.FinishReason(""), e.Err
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
	traceMeta *debug.TraceMeta,
) {
	startedToolIDs := make(map[string]bool)
	for {
		event, err := stream.Next()
		if err != nil {
			// Finalize pending tool calls before returning
			for id := range startedToolIDs {
				_, block, _ := toolAcc.Done(litellm.ToolUseDone{ID: id})
				if block != nil {
					*toolCalls = append(*toolCalls, *block)
				}
			}
			return
		}
		switch e := event.(type) {
		case litellm.ContentDelta:
			content.WriteString(e.Text)
			sseWriter.Token(e.Text)
		case litellm.ReasoningDelta:
			sseWriter.ThinkingDelta(e.Text)
		case litellm.ToolUseStart:
			if e.ID != "" {
				startedToolIDs[e.ID] = true
			}
			toolAcc.Start(e)
		case litellm.ToolUseDelta:
			toolAcc.Delta(e)
		case litellm.ToolUseDone:
			_, block, _ := toolAcc.Done(e)
			if block != nil {
				*toolCalls = append(*toolCalls, *block)
			}
			delete(startedToolIDs, e.ID)
		case litellm.UsageEvent:
			*usage = &e.Usage
			if traceMeta != nil {
				traceMeta.SetUsage(debugUsageFromLitellm(*usage))
				if e.Usage.Model != "" {
					traceMeta.SetModel(e.Usage.Model)
				}
			}
		case litellm.DoneEvent:
			// Finalize pending tool calls before returning
			for id := range startedToolIDs {
				_, block, _ := toolAcc.Done(litellm.ToolUseDone{ID: id})
				if block != nil {
					*toolCalls = append(*toolCalls, *block)
				}
			}
			if traceMeta != nil {
				traceMeta.SetFinishReason(string(e.FinishReason))
				if e.Model != "" {
					traceMeta.SetModel(e.Model)
				}
			}
			return
		case litellm.ErrorEvent:
			return
		}
	}
}

// MaxTurnsExceededError reports that a run hit its configured turn cap.
type MaxTurnsExceededError struct {
	Limit int
}

func (e *MaxTurnsExceededError) Error() string {
	return fmt.Sprintf("max turns limit reached: %d", e.Limit)
}

// MaxOutputTokensExceededError reports that the model exhausted its per-turn
// output-token budget (finish_reason="length") before emitting a tool call or
// final answer. The turn was truncated by the provider. This is surfaced as an
// error rather than a clean completion so a truncated reasoning turn is never
// silently swallowed as "done".
type MaxOutputTokensExceededError struct {
	Limit int
}

func (e *MaxOutputTokensExceededError) Error() string {
	return fmt.Sprintf("max output tokens limit reached: %d (truncated response)", e.Limit)
}

// TurnTimeoutError reports that a single LLM turn exceeded its per-turn wall-clock
// deadline (e.g. the provider streamed reasoning/thinking tokens for the whole
// window without ever emitting content or a tool call). Surfaced as an error so
// a stalled turn is never silently swallowed as a clean completion, and is not
// retried (a retry would just burn another timeout window on the same stall).
type TurnTimeoutError struct {
	Timeout time.Duration
}

func (e *TurnTimeoutError) Error() string {
	return fmt.Sprintf("turn timed out after %s (no content or tool call received)", e.Timeout)
}

// debugUsageFromLitellm converts provider usage parsed from a litellm stream
// into the UsageTotals shape recorded on HTTP traces.
func debugUsageFromLitellm(u *litellm.Usage) *debug.UsageTotals {
	if u == nil {
		return nil
	}
	return &debug.UsageTotals{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		ReasoningTokens:  u.ReasoningTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
	}
}

// usageForDone converts provider-reported usage into the TokenUsage carried on
// the done SSE event when the provider reported any. When the provider returned
// no usage, it falls back to the text-length estimate. This guarantees the
// provider-reported value is never shadowed by the estimate on the done path.
func usageForDone(usage *litellm.Usage, text string, store *tokenizer.CalibrationStore, model string) *tokenizer.TokenUsage {
	if usage != nil && usage.HasTokens() {
		total := usage.TotalTokens
		if total == 0 {
			total = usage.InputTokens + usage.OutputTokens
		}
		return &tokenizer.TokenUsage{
			TotalTokens:      total,
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
		}
	}
	return tokenizer.EstimateUsage(text, store, model)
}

// updateCalibration feeds provider usage data from a completed LLM response
// into the CalibrationStore. It computes chars_per_token = inputLen / promptTokens
// and calls store.Update(model, cpt).
func updateCalibration(store *tokenizer.CalibrationStore, model string, messages []litellm.Message, usage *litellm.Usage) {
	if store == nil || model == "" || usage == nil || usage.InputTokens <= 0 {
		return
	}

	// Compute total input text length from the messages sent in the request.
	inputLen := 0
	for _, msg := range messages {
		inputLen += len(litellmMsgText(msg))
	}
	if inputLen == 0 {
		return
	}
	// Reject physically implausible observations. Even CJK-heavy payloads
	// tokenize to >1 char/token; anything below 1.0 means the measured input
	// length and the provider token count are not measuring the same bytes
	// (e.g. a measurement bug), and accepting it would poison every later
	// context estimate for the model.
	cpt := float64(inputLen) / float64(usage.InputTokens)
	if cpt < 1.0 {
		slog.Warn("calibration observation rejected: chars/token below 1.0 is implausible",
			slog.String("model", model),
			slog.Float64("observed_cpt", cpt),
			slog.Int("input_chars", inputLen),
			slog.Int("prompt_tokens", usage.InputTokens),
		)
		return
	}

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
