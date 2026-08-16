package engine

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/provider"
)

// mergedMarkerRe matches the merged tail marker CapBytes emits when a draft is
// both line-truncated (compressor) and byte-truncated (engine cap).
var mergedMarkerRe = regexp.MustCompile(`\+[0-9]+ more, \+[0-9]+ bytes truncated\n$`)

// byteCapToolDefs returns the tool manifest the byte-cap agent tests drive:
// bash, read, and web_fetch each with the strict-shaped schema the engine's
// dispatch path validates before execution.
func byteCapToolDefs() []provider.Tool {
	return []provider.Tool{
		{Type: "function", Function: provider.ToolFunction{
			Name: "bash", Description: "run shell",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"command": map[string]any{"type": "string"},
			}, "required": []any{"command"}},
		}},
		{Type: "function", Function: provider.ToolFunction{
			Name: "read", Description: "read file",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string"},
			}, "required": []any{"path"}},
		}},
		{Type: "function", Function: provider.ToolFunction{
			Name: "web_fetch", Description: "fetch url",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"},
			}, "required": []any{"url"}},
		}},
	}
}

// hugeDraft builds a multi-hundred-KiB tool-result draft, the shape that could
// otherwise exhaust the context window.
func hugeDraft(prefix string) string {
	return strings.Repeat(prefix+" payload line number "+
		strings.Repeat("x", 40)+"\n", 8000) // ~450 KiB
}

// TestAgentToolResultsByteCappedInHistory drives a bash turn whose raw result
// is a multi-hundred-KiB listing and asserts every tool message the provider
// receives carries the byte-capped delivered form — never the oversized raw
// string — while the ToolResultEvent keeps the full raw result (expand path).
func TestAgentToolResultsByteCappedInHistory(t *testing.T) {
	raw := hugeDraft("item")
	capLower := compress.DefaultByteCap

	var gotResult *ToolResultEvent
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls -R ."}`},
				}, Done: true},
			), nil
		}
		// Every tool message in history must be the capped form: bounded and
		// never the raw string.
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				if len(m.Content) > capLower {
					t.Errorf("tool message in history is %d bytes, exceeds %d-byte cap", len(m.Content), capLower)
				}
				if m.Content == raw {
					t.Error("oversized raw string reached the provider message history")
				}
				if !strings.Contains(m.Content, " bytes truncated") {
					t.Errorf("tool message in history missing explicit byte marker: %q", m.Content[len(m.Content)-40:])
				}
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	})

	e := New(scripted, &mockTranscript{})
	e.SetListener(func(evt Event) {
		if tr, ok := evt.(ToolResultEvent); ok {
			gotResult = &tr
		}
	})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "list"},
		AgentOptions{
			Tools:    byteCapToolDefs(),
			Executor: hugeExecutor(map[string]string{"bash": raw}),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	if gotResult == nil {
		t.Fatal("no ToolResultEvent emitted")
	}
	// Expand path intact: the event carries the FULL pre-cap raw result.
	if gotResult.Result != raw {
		t.Errorf("ToolResultEvent.Result is not the full raw result (len=%d, want %d)",
			len(gotResult.Result), len(raw))
	}
	if gotResult.Delivered == raw {
		t.Error("ToolResultEvent.Delivered must be the capped form, not the raw result")
	}
	if len(gotResult.Delivered) > compress.DefaultByteCap {
		t.Errorf("ToolResultEvent.Delivered = %d bytes, exceeds %d-byte cap",
			len(gotResult.Delivered), compress.DefaultByteCap)
	}
	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
	if !strings.Contains(gotResult.Delivered, " bytes truncated") {
		t.Errorf("Delivered missing explicit byte marker: %q", gotResult.Delivered[len(gotResult.Delivered)-40:])
	}
}

// TestAgentByteCapComposesWithLineMarker drives a read turn whose tool result
// is already line-truncated by the compressor (explicit "+N more" tail); the
// engine byte-cap must compose the two markers into a single merged tail line
// in history, and the event's line metadata must still derive from the full
// pre-cap result (Dropped line count preserved).
func TestAgentByteCapComposesWithLineMarker(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30000; i++ {
		b.WriteString("entry.")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" payload\n")
	}
	b.WriteString("+29900 more\n")
	draft := b.String() // ~330 KiB, line-truncated to 100 lines + marker

	var gotResult *ToolResultEvent
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_read", Name: "read", Arguments: `{"path":"big.txt"}`},
				}, Done: true},
			), nil
		}
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				if len(m.Content) > compress.DefaultByteCap {
					t.Errorf("tool message in history = %d bytes, exceeds cap", len(m.Content))
				}
				if !mergedMarkerRe.MatchString(m.Content) {
					t.Errorf("history tool message missing merged marker line, tail: %q",
						m.Content[len(m.Content)-60:])
				}
				if m.Content == draft {
					t.Error("raw pre-cap draft reached the provider message history")
				}
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	})

	e := New(scripted, &mockTranscript{})
	e.SetListener(func(evt Event) {
		if tr, ok := evt.(ToolResultEvent); ok {
			gotResult = &tr
		}
	})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "read"},
		AgentOptions{
			Tools:    byteCapToolDefs(),
			Executor: hugeExecutor(map[string]string{"read": draft}),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	if gotResult == nil {
		t.Fatal("no ToolResultEvent emitted")
	}
	// Expand path: full pre-cap result (line-truncated draft) preserved.
	if gotResult.Result != draft {
		t.Errorf("ToolResultEvent.Result is not the full pre-cap draft (len=%d, want %d)",
			len(gotResult.Result), len(draft))
	}
	// Line metadata derives from the FULL pre-cap result exactly as before the
	// byte-cap existed: the full draft is 30000 entries + the "+29900 more"
	// marker line, so Lines counts the whole draft while Dropped reads the
	// marker's line count.
	if gotResult.Dropped != 29900 {
		t.Errorf("ToolResultEvent.Dropped = %d, want 29900 (line marker of full result)", gotResult.Dropped)
	}
	if !gotResult.Compressed {
		t.Error("ToolResultEvent.Compressed = false, want true (line-truncated full result)")
	}
	if gotResult.Lines != 30001 {
		t.Errorf("ToolResultEvent.Lines = %d, want 30001 (30000 entries + marker of the full draft)", gotResult.Lines)
	}
	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
	if !mergedMarkerRe.MatchString(gotResult.Delivered) {
		t.Errorf("Delivered missing merged marker line, tail: %q",
			gotResult.Delivered[len(gotResult.Delivered)-60:])
	}
}

// TestAgentByteCapWebFetchKeepsExpandPath drives a web_fetch turn whose raw
// page Markdown is huge; the delivered form is capped while the event carries
// the full raw result (the TUI expand seam stays lossless).
func TestAgentByteCapWebFetchKeepsExpandPath(t *testing.T) {
	raw := hugeDraft("page")

	var gotResult *ToolResultEvent
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_fetch", Name: "web_fetch", Arguments: `{"url":"https://example.com/"}`},
				}, Done: true},
			), nil
		}
		// The provider must never receive the oversized raw page.
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool && m.Content == raw {
				t.Error("oversized raw web_fetch result reached the provider message history")
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	})

	e := New(scripted, &mockTranscript{})
	e.SetListener(func(evt Event) {
		if tr, ok := evt.(ToolResultEvent); ok {
			gotResult = &tr
		}
	})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "fetch"},
		AgentOptions{
			Tools:    byteCapToolDefs(),
			Executor: hugeExecutor(map[string]string{"web_fetch": raw}),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	if gotResult == nil {
		t.Fatal("no ToolResultEvent emitted")
	}
	if gotResult.Result != raw {
		t.Errorf("ToolResultEvent.Result is not the full raw page (len=%d, want %d)",
			len(gotResult.Result), len(raw))
	}
	if len(gotResult.Delivered) > compress.DefaultByteCap {
		t.Errorf("Delivered = %d bytes, exceeds %d-byte cap", len(gotResult.Delivered), compress.DefaultByteCap)
	}
	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
}

// hugeExecutor returns a ToolExecutor that serves the canned huge draft for
// the named tools, so the byte-cap tests exercise the engine boundary without
// filesystem/network side effects.
func hugeExecutor(results map[string]string) ToolExecutor {
	return ExecutorFunc(func(_ context.Context, name, _ string) (string, error) {
		if r, ok := results[name]; ok {
			return r, nil
		}
		return "result:" + name, nil
	})
}
