package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	vocellitellm "github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/tool"
)

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
				if evt.IsReasoning {
					// Reasoning content from adapters is clean text — the IsReasoning
					// flag is the sole discriminator (no delimiter tags expected).
					sseWriter.ThinkingDelta(evt.Content)
				} else {
					content.WriteString(evt.Content)
					sseWriter.Token(evt.Content)
				}

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

// toolLister is the interface for listing tool definitions, used by toolDefsFromRegistry.
// *tool.Registry satisfies this interface.
type toolLister interface {
	LitellmTools() []vocellitellm.Tool
}

// toolDefsFromRegistry converts tool definitions from a tool lister to internal ToolDefs.
func toolDefsFromRegistry(reg toolLister) []litellm.ToolDef {
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
	"edit":                  "FileEditCard",
}

// emitComponentForTool emits a component event based on the tool name and args.
// Supported tools: render_mermaid_diagram, render_quick_replies, edit.
// The edit tool emits a FileEditCard using old_text/new_text/path from args.
// QuickReplies does NOT emit a component SSE event (chips are stored inline on the message).
// Returns (componentName, data, ok) for the caller to also persist the component.
func emitComponentForTool(w *runstate.Writer, toolName string, args json.RawMessage, blocks []vocellitellm.Block) (string, map[string]interface{}, bool) {
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
		// QuickReplies renders inline — no separate SSE component event
		return componentName, data, true

	case "FileEditCard":
		var parsed struct {
			Path    string `json:"path"`
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil {
			return "", nil, false
		}
		if parsed.Path == "" {
			return "", nil, false
		}
		if parsed.OldText == "" && parsed.NewText == "" {
			return "", nil, false
		}
		fullOld := parsed.OldText
		fullNew := parsed.NewText
		data["path"] = parsed.Path
		data["mode"] = "overwrite"
		data["old"] = fullOld
		data["new"] = fullNew
		data["bytes_written"] = len(parsed.NewText)
		// Note: dirs_created always empty for edit tool

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

// addReadToolAllowedPath looks up the ReadTool in the registry and appends a path
// to its temporary allowed paths list. Used by the agent loop when a user approves
// a blocked read path so the tool can re-execute without another confirmation.
func addReadToolAllowedPath(tools *tool.Registry, path string) {
	h := tools.Lookup("read")
	if h == nil {
		return
	}
	rt, ok := h.(*tool.ReadTool)
	if !ok {
		return
	}
	rt.AppendAllowedPaths(path)
}

// dumpRequestOnError writes the full chat request as JSON to the debug directory
// when EITRI_DEBUG_LLM_DIR is set and an LLM request fails.
func dumpRequestOnError(req *litellm.Request, err error, attempt int) {
	dir := os.Getenv("EITRI_DEBUG_LLM_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("cannot create LLM debug dir", slog.String("dir", dir), slog.Any("error", err))
		return
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("runner-llm-request-%d-attempt-%d.json", timestamp, attempt)
	path := filepath.Join(dir, filename)

	type debugEntry struct {
		Request litellm.Request `json:"request"`
		Error   string          `json:"error"`
		Attempt int             `json:"attempt"`
	}

	entry := debugEntry{
		Request: *req,
		Error:   err.Error(),
		Attempt: attempt,
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal LLM request dump", slog.Any("error", err))
		return
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("failed to write LLM request dump", slog.String("path", path), slog.Any("error", err))
		return
	}

	slog.Warn("LLM request dump written", slog.String("path", path), slog.Int("attempt", attempt))
}

// isRetryableLLMError checks if an LLM error is likely transient and worth retrying.
// Returns false for auth errors, context-length errors, rate-limits, and context cancellation.
func isRetryableLLMError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := err.Error()
	// Don't retry auth errors
	if strings.Contains(msg, "401") || strings.Contains(msg, "Authentication") {
		return false
	}
	// Don't retry rate limits
	if strings.Contains(msg, "429") || strings.Contains(msg, "Rate limit") {
		return false
	}
	// Don't retry context length errors
	if strings.Contains(msg, "Context length") || strings.Contains(msg, "context length") {
		return false
	}
	// Don't retry bad request (400) — indicates a problem with the request itself
	// that won't resolve by re-sending (e.g. unknown model, invalid parameters).
	if strings.Contains(msg, "400") || strings.Contains(msg, "Bad Request") {
		return false
	}
	// Everything else (5xx, upstream failures, connection errors) is potentially transient
	return true
}
