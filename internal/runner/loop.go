package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
)

// ConfirmationResult carries the user's decision for a confirmation prompt.
type ConfirmationResult struct {
	Path     string
	Approved bool
}

// ConfirmationFunc is called when a tool needs user confirmation before
// proceeding. It sends the confirmation request and blocks until the user
// responds or the context is cancelled.
type ConfirmationFunc func(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)

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
//
// contextWindow is the configured context window token limit. When > 0,
// context_update SSE events are broadcast after each turn. When <= 0,
// no context_update events are emitted.
//
// historyMgr handles reading and appending conversation history. Two concrete
// types exist: sessionHistoryManager (browser UI path) and requestHistoryManager
// (headless/direct-messages path).
//
// confirmer handles user confirmation for path-based tool access. When nil,
// confirmation-dependent operations return errors to the LLM.
func RunAgent(
	ctx context.Context,
	svc llm.LLMService,
	req *llm.Request,
	maxTurns int,
	maxHistory int,
	sseWriter *runstate.Writer,
	tools *tool.Registry,
	historyMgr HistoryManager,
	confirmer Confirmer,
	uisessionMgr *uisession.Manager,
	sessionID string,
	contextWindow int,
	crashDumpFunc func(err error, stack []byte), // optional; called on panic
	turns *int, // updated each turn with the current turn count
) error {
	if maxTurns <= 0 {
		maxTurns = 10
	}

	// Panic recovery: write crash dump then re-panic
	if crashDumpFunc != nil {
		defer func() {
			if r := recover(); r != nil {
				var err error
				switch x := r.(type) {
				case error:
					err = x
				default:
					err = fmt.Errorf("panic: %v", x)
				}
				crashDumpFunc(err, runtimeDebug.Stack())
				panic(r)
			}
		}()
	}

	// Helper to broadcast context_update if enabled and historyMgr is available.
	broadcastContextUpdate := func() {
		if contextWindow <= 0 {
			return
		}
		// Only broadcast for session-based (UI) history, not request-based.
		if _, ok := historyMgr.(*requestHistoryManager); ok {
			return
		}
		history := historyMgr.History(sessionID)
		if history == nil {
			return
		}
		update := runstate.ComputeContext(history, contextWindow)
		sseWriter.ContextUpdate(update)
	}

	req.Stream = true

	// Compute tool definitions once before the loop, then reuse on every turn.
	// This avoids re-iterating the registry and allocating new slices per turn.
	var toolDefs []llm.ToolDef
	if tools != nil {
		toolDefs = toolDefsFromRegistry(tools)
	}

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Load conversation history via adapter
		req.Messages = historyMgr.History(sessionID)

		// Attach tool definitions (computed once before the loop)
		if toolDefs != nil {
			req.Tools = toolDefs
		}

		slog.Debug("llm turn", slog.Int("turn", turn), slog.Int("tools", len(req.Tools)), slog.Int("messages", len(req.Messages)))

		// Call LLM streaming with retry on transient errors
		const maxRetries = 5
		var (
			stream <-chan llm.StreamEvent
			err    error
		)
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stream, err = svc.ChatStream(ctx, *req)
			if err == nil {
				break
			}
			if attempt < maxRetries && isRetryableLLMError(err) {
				slog.Warn("llm chat stream transient error, retrying",
					slog.Int("attempt", attempt+1),
					slog.Int("max", maxRetries),
					slog.Any("error", err),
				)
				dumpRequestOnError(req, err, attempt+1)
				time.Sleep(1 * time.Second)
				continue
			}
			dumpRequestOnError(req, err, maxRetries+1)
			msg := fmt.Sprintf("LLM error: %v", err)
			sseWriter.Error(msg)
			return fmt.Errorf("chat stream: %w", err)
		}

		// Process stream events
		content, toolCalls, streamErr := drainStream(ctx, stream, sseWriter)
		if streamErr != nil {
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				// Preserve partial result: append assistant message with accumulated
				// content and any tool calls to conversation history before returning.
				// Always save even when empty (e.g. thinking-only stream) to maintain
				// user→assistant→user alternation — otherwise next user message creates
				// consecutive user messages which some providers reject as malformed.
				historyMgr.AppendAssistant(sessionID, content.String(), toolCalls)
				if isRequestBasedHistory(historyMgr) {
					trimMessages(req, maxHistory)
				}
				return streamErr
			}
			sseWriter.Error(runstate.FormatErrorMessage(streamErr))
			return streamErr
		}
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

			usage := runstate.EstimateUsage(contentStr)
			sseWriter.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), usage)
			// Append final assistant response to conversation history
			if contentStr != "" || len(req.Messages) > 0 {
				historyMgr.AppendAssistant(sessionID, contentStr, nil)
			}
			// Trim conversation history if cap is set (only when not using session manager)
			if isRequestBasedHistory(historyMgr) {
				trimMessages(req, maxHistory)
			}
			return nil
		}

		// Trim conversation history if cap is set (only when not using session manager)
		if isRequestBasedHistory(historyMgr) {
			trimMessages(req, maxHistory)
		}

		// Has tool calls — add assistant message to history
		historyMgr.AppendAssistant(sessionID, content.String(), toolCalls)

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
			sseWriter.ToolCall(tc.Function.Name, argsForDisplay)

			// Dispatch tool via registry
			dispResult, dispErr := tools.Dispatch(ctx, tc.ID, tc.Function.Name, args)
			if dispErr != nil {
				// Feed unknown tool / dispatch errors back to the LLM as tool
				// result instead of terminating the loop. LLMs commonly hallucinate
				// tool names (e.g. "replace" instead of "edit") — this gives them
				// a chance to self-correct on the next turn.
				errMsg := fmt.Sprintf("Tool error: %v", dispErr)
				// Broadcast tool result so the error shows in the tool card
				// (not as a separate error toast that closes the stream).
				sseWriter.ToolResult(tc.Function.Name, errMsg)
				// Record the error as a tool result so the LLM can see it
				historyMgr.AppendTool(sessionID, tc.ID, errMsg, true)
				slog.Warn("tool dispatch error", slog.String("tool", tc.Function.Name), slog.String("error", errMsg))
				continue
			}

			// Check if tool needs user confirmation
			if dispResult.NeedsConfirm && confirmer != nil {
				confPath := dispResult.ConfirmPath
				confMsg := dispResult.ConfirmMessage
				slog.Debug("tool needs confirmation", slog.String("path", confPath), slog.String("message", confMsg))

				// Send needs_confirmation SSE event
				sseWriter.State().Broadcast(runstate.SSEEvent{
					Type:    "needs_confirmation",
					Content: confMsg,
					Data:    map[string]any{"path": confPath, "message": confMsg},
				})

				// Wait for user response
				confirmResult, confirmErr := confirmer.Confirm(ctx, sessionID, confPath, confMsg)
				if confirmErr != nil {
					if errors.Is(confirmErr, context.Canceled) || errors.Is(confirmErr, context.DeadlineExceeded) {
						return confirmErr
					}
					errMsg := fmt.Sprintf("Confirmation error: %v", confirmErr)
					sseWriter.ToolResult(tc.Function.Name, errMsg)
					historyMgr.AppendTool(sessionID, tc.ID, errMsg, true)
					continue
				}

				if confirmResult.Approved {
					// Temporarily add the path to ReadTool's allowedPaths
					// and re-dispatch
					addReadToolAllowedPath(tools, confPath)
					dispResult, dispErr = tools.Dispatch(ctx, tc.ID, tc.Function.Name, args)
					if dispErr != nil {
						errMsg := fmt.Sprintf("Tool error after approval: %v", dispErr)
						sseWriter.ToolResult(tc.Function.Name, errMsg)
						historyMgr.AppendTool(sessionID, tc.ID, errMsg, true)
						continue
					}
					// Continue to process blocks below (resultText, Broadcast, etc.)
				} else {
					// Denial — return error to LLM
					errMsg := "Access denied to path: " + confPath
					sseWriter.ToolResult(tc.Function.Name, errMsg)
					historyMgr.AppendTool(sessionID, tc.ID, errMsg, true)
					continue
				}
			}

			// Extract result text from blocks
			blocks := dispResult.Blocks
			resultText := blocksToText(blocks)
			isError := toolResultHasError(blocks)
			slog.Debug("tool result", slog.String("tool", tc.Function.Name), slog.String("result", truncateText(resultText, 200)), slog.Bool("error", isError))

			// Broadcast tool result event
			sseWriter.ToolResult(tc.Function.Name, resultText)

			// Broadcast skill_activated if this was a successful skill load
			if tc.Function.Name == "skill" && !isError {
				// Extract the skill name from the tool call arguments
				var skillArgs struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(args, &skillArgs); err == nil && skillArgs.Name != "" {
					sseWriter.SkillActivated(skillArgs.Name)
				}
			}

			// Emit component event for compatible tools (except QuickReplies which stores inline)
			if !isError || tc.Function.Name == "render_quick_replies" {
				compName, compData, ok := emitComponentForTool(sseWriter, tc.Function.Name, args, blocks)
				if ok && uisessionMgr != nil {
					if tc.Function.Name == "render_quick_replies" {
						// QuickReplies stores inline on the assistant message, not as a component event
						if opts, ok := compData["options"]; ok {
							if optStrs, ok := opts.([]string); ok {
								_ = uisessionMgr.SetQuickReplies(sessionID, optStrs)
							}
						}
					} else {
						_ = uisessionMgr.AppendComponent(sessionID, uisession.ComponentData{
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
			historyMgr.AppendTool(sessionID, tc.ID, resultContent, isError)
		}

		// Broadcast context_update after tool results appended to history
		broadcastContextUpdate()

		// Update turn count for external consumers
		if turns != nil {
			*turns = turn + 1
		}
	}

	// Max turns exceeded
	// Broadcast final context_update before error
	broadcastContextUpdate()
	msg := runstate.MaxTurnsMessage(maxTurns)
	sseWriter.Error(msg)
	return &MaxTurnsExceededError{Limit: maxTurns}
}

// isRequestBasedHistory returns true when the history manager is the
// request-based variant, meaning history is stored directly on the
// *llm.Request and must be trimmed by RunAgent when caps are set.
func isRequestBasedHistory(mgr HistoryManager) bool {
	_, ok := mgr.(*requestHistoryManager)
	return ok
}
