package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	vocellitellm "github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
)

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
func RunAgent(
	ctx context.Context,
	llm litellm.LLMService,
	req *litellm.Request,
	maxTurns int,
	maxHistory int,
	sseWriter *runstate.Writer,
	tools *tool.Registry,
	sessionMgr *history.SessionManager,
	uisessionMgr *uisession.Manager,
	sessionID string,
) error {
	if maxTurns <= 0 {
		maxTurns = 10
	}

	req.Stream = true

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Load conversation history from session manager when available
		if sessionMgr != nil {
			req.Messages = sessionMgr.History(sessionID)
		}

		// Attach tool definitions from registry
		if tools != nil {
			req.Tools = toolDefsFromRegistry(tools)
		}

		slog.Debug("llm turn", slog.Int("turn", turn), slog.Int("tools", len(req.Tools)), slog.Int("messages", len(req.Messages)))

		// Call LLM streaming
		stream, err := llm.ChatStream(ctx, *req)
		if err != nil {
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
				if content.Len() > 0 || len(toolCalls) > 0 {
					if sessionMgr != nil {
						sessionMgr.AppendAssistant(sessionID, content.String(), toolCalls)
					} else {
						req.Messages = append(req.Messages, litellm.Message{
							Role:      "assistant",
							Content:   content.String(),
							ToolCalls: toolCalls,
						})
						trimMessages(req, maxHistory)
					}
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
			usage := runstate.EstimateUsage(contentStr)
			sseWriter.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), usage)
			// Append final assistant response to conversation history
			if contentStr != "" || len(req.Messages) > 0 {
				if sessionMgr != nil {
					sessionMgr.AppendAssistant(sessionID, contentStr, nil)
				} else {
					req.Messages = append(req.Messages, litellm.Message{
						Role:    "assistant",
						Content: contentStr,
					})
				}
			}
			// Trim conversation history if cap is set (only when not using session manager)
			if sessionMgr == nil {
				trimMessages(req, maxHistory)
			}
			return nil
		}

		// Trim conversation history if cap is set (only when not using session manager)
		if sessionMgr == nil {
			trimMessages(req, maxHistory)
		}

		// Has tool calls — add assistant message to history
		if sessionMgr != nil {
			sessionMgr.AppendAssistant(sessionID, content.String(), toolCalls)
		} else {
			req.Messages = append(req.Messages, litellm.Message{
				Role:      "assistant",
				Content:   content.String(),
				ToolCalls: toolCalls,
			})
		}

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
			sseWriter.ToolCall(tc.Function.Name, json.RawMessage(tc.Function.Arguments))

			// Dispatch tool via registry
			blocks, err := tools.Dispatch(ctx, tc.ID, tc.Function.Name, args)
			if err != nil {
				// Feed unknown tool / dispatch errors back to the LLM as tool
				// result instead of terminating the loop. LLMs commonly hallucinate
				// tool names (e.g. "replace" instead of "edit") — this gives them
				// a chance to self-correct on the next turn.
				errMsg := fmt.Sprintf("Tool error: %v", err)
				// Broadcast tool result so the error shows in the tool card
				// (not as a separate error toast that closes the stream).
				sseWriter.ToolResult(tc.Function.Name, errMsg)
				// Record the error as a tool result so the LLM can see it
				if sessionMgr != nil {
					sessionMgr.AppendTool(sessionID, tc.ID, errMsg, true)
				} else {
					req.Messages = append(req.Messages, litellm.Message{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    errMsg,
					})
				}
				slog.Warn("tool dispatch error", slog.String("tool", tc.Function.Name), slog.String("error", errMsg))
				continue
			}

			// Extract result text from blocks
			resultText := blocksToText(blocks)
			isError := toolResultHasError(blocks)
			slog.Debug("tool result", slog.String("tool", tc.Function.Name), slog.String("result", truncateText(resultText, 200)), slog.Bool("error", isError))

			// Broadcast tool result event
			sseWriter.ToolResult(tc.Function.Name, resultText)

			// Emit component event for compatible tools
			if !isError {
				compName, compData, ok := emitComponentForTool(sseWriter, tc.Function.Name, args)
				if ok && uisessionMgr != nil {
					uisessionMgr.AppendComponent(sessionID, uisession.ComponentData{
						Name: compName,
						Data: compData,
					})
				}
			}

			// Add tool result message to conversation history
			resultContent := resultText
			if isError && resultContent == "" {
				resultContent = fmt.Sprintf("Error executing %q", tc.Function.Name)
			}
			if sessionMgr != nil {
				sessionMgr.AppendTool(sessionID, tc.ID, resultContent, isError)
			} else {
				req.Messages = append(req.Messages, litellm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    resultContent,
				})
			}
		}
	}

	// Max turns exceeded
	msg := runstate.MaxTurnsMessage(maxTurns)
	sseWriter.Error(msg)
	return &MaxTurnsExceededError{Limit: maxTurns}
}

// trimMessages removes the oldest message pairs when total non-system messages
// exceed maxHistory. System prompt is always preserved.
// maxHistory of 0 means no limit.
func trimMessages(req *litellm.Request, maxHistory int) {
	if maxHistory <= 0 {
		return
	}

	// Count non-system messages
	nonSysCount := 0
	for _, msg := range req.Messages {
		if msg.Role != "system" {
			nonSysCount++
		}
	}

	if nonSysCount <= maxHistory {
		return
	}

	toRemove := nonSysCount - maxHistory

	// Build new slice preserving system prompt(s) and the most recent messages
	var kept []litellm.Message
	var removed int
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			kept = append(kept, msg)
			continue
		}
		if removed < toRemove {
			removed++
			continue
		}
		kept = append(kept, msg)
	}
	req.Messages = kept
}

// drainStream reads all events from a stream channel and collects text content
// and tool calls. Token events are forwarded to the SSE writer.
func drainStream(
	ctx context.Context,
	stream <-chan litellm.StreamEvent,
	sseWriter *runstate.Writer,
) (*strings.Builder, []litellm.ToolCall, error) {
	var content strings.Builder
	var toolCalls []litellm.ToolCall

	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return &content, toolCalls, nil
			}

			switch evt.Type {
			case litellm.StreamEventTypeToken:
				content.WriteString(evt.Content)
				sseWriter.Token(evt.Content)

			case litellm.StreamEventTypeToolCall:
				if len(evt.ToolCalls) > 0 {
					toolCalls = evt.ToolCalls
				}

			case litellm.StreamEventTypeDone:
				return &content, toolCalls, nil

			case litellm.StreamEventTypeError:
				if evt.Error != nil {
					return &content, toolCalls, evt.Error
				}
				return &content, toolCalls, nil
			}

		case <-ctx.Done():
			return &content, toolCalls, ctx.Err()
		}
	}
}

// toolDefsFromRegistry converts tool registry tools to internal ToolDefs.
func toolDefsFromRegistry(reg *tool.Registry) []litellm.ToolDef {
	vooTools := reg.LitellmTools()
	defs := make([]litellm.ToolDef, len(vooTools))
	for i, t := range vooTools {
		defs[i] = litellm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(t.Parameters),
		}
	}
	return defs
}

// blocksToText extracts text content from a slice of voocel/litellm blocks.
func blocksToText(blocks []vocellitellm.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		switch v := block.(type) {
		case vocellitellm.TextBlock:
			b.WriteString(v.Text)
		case vocellitellm.ToolResultBlock:
			b.WriteString(blocksToText(v.Content))
		default:
			b.WriteString(fmt.Sprintf("%v", block))
		}
	}
	return b.String()
}

// toolResultHasError checks if the first block is a ToolResultBlock with IsError=true.
func toolResultHasError(blocks []vocellitellm.Block) bool {
	if len(blocks) == 0 {
		return false
	}
	if tr, ok := blocks[0].(vocellitellm.ToolResultBlock); ok {
		return tr.IsError
	}
	return false
}

// componentToolMap maps tool names to component names for component emission.
var componentToolMap = map[string]string{
	"render_mermaid_diagram": "MermaidDiagram",
	"render_quick_replies":  "QuickReplies",
	"render_diff_card":      "DiffCard",
	"edit":                  "DiffCard",
}

// emitComponentForTool emits a component event based on the tool name and args.
// Supported tools: render_mermaid_diagram, render_quick_replies, render_diff_card, edit.
// The edit tool emits a DiffCard using old_text/new_text from args.
// Returns (componentName, data, ok) for the caller to also persist the component.
func emitComponentForTool(w *runstate.Writer, toolName string, args json.RawMessage) (string, map[string]interface{}, bool) {
	componentName, ok := componentToolMap[toolName]
	if !ok {
		return "", nil, false
	}

	data := make(map[string]interface{})

	switch componentName {
	case "MermaidDiagram":
		var parsed struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil || parsed.Code == "" {
			return "", nil, false
		}
		data["code"] = parsed.Code

	case "QuickReplies":
		var parsed struct {
			Options []string `json:"options"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil || len(parsed.Options) == 0 {
			return "", nil, false
		}
		data["options"] = parsed.Options

	case "DiffCard":
		if toolName == "edit" {
			// Read old/new from edit tool's typed args (deterministic, no text parsing)
			var parsed struct {
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := json.Unmarshal(args, &parsed); err != nil {
				return "", nil, false
			}
			if parsed.OldText == "" && parsed.NewText == "" {
				return "", nil, false
			}
			data["old"] = parsed.OldText
			data["new"] = parsed.NewText
			data["lang"] = ""
		} else {
			// render_diff_card: read from its args
			var parsed struct {
				Old  string `json:"old"`
				New  string `json:"new"`
				Lang string `json:"lang,omitempty"`
			}
			if err := json.Unmarshal(args, &parsed); err != nil {
				return "", nil, false
			}
			if parsed.Old == "" && parsed.New == "" {
				return "", nil, false
			}
			data["old"] = parsed.Old
			data["new"] = parsed.New
			data["lang"] = parsed.Lang
		}

	default:
		return "", nil, false
	}

	w.Component(map[string]interface{}{
		"kind": "component",
		"name": componentName,
		"data": data,
	})
	return componentName, data, true
}

// truncateText truncates s to at most n runes, appending "..." when truncated.
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
