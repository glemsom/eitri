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
	t.Parallel()
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
	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
}

// TestAgentByteCapComposesWithLineMarker drives a read turn whose tool result
// is already line-truncated by the compressor (explicit "+N more" tail); the
// engine byte-cap must compose the two markers into a single merged tail line
// in history, and the event's line metadata must still derive from the full
// pre-cap result (Dropped line count preserved).
func TestAgentByteCapComposesWithLineMarker(t *testing.T) {
	t.Parallel()
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
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls -R ."}`},
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

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "list"},
		AgentOptions{
			Tools: byteCapToolDefs(),
			Executor: ExecutorFunc(func(_ context.Context, name, _ string) (ToolExecResult, error) {
				if name == "bash" {
					// The compressor's form: line-truncated with the explicit
					// "+N more" tail, reported compressed=true so the byte-cap
					// merges the two markers.
					return ToolExecResult{Text: draft, Compressed: true}, nil
				}
				return ToolExecResult{Text: "result:" + name}, nil
			}),
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
	if gotResult.Dropped != 29900 {
		t.Errorf("ToolResultEvent.Dropped = %d, want 29900", gotResult.Dropped)
	}
}

// TestAgentByteCapPreservesLookLikeMarkerContent drives a read turn whose raw
// tool result legitimately ends in a line that LOOKS like the line-compressor's
// "+N more" marker (e.g. a file whose literal last line matches, or a web page
// ending in "+12 more") — content the byte-cap must never silently strip as a
// "marker". Only the byte-cap's plain "+N bytes truncated" tail may be
// appended; the look-like-marker content line must survive in the delivered
// form, byte-dropped only at the head budget.
func TestAgentByteCapPreservesLookLikeMarkerContent(t *testing.T) {
	t.Parallel()
	// Over-budget so the byte-cap actually runs; the raw content ends with a
	// literal "+300 more" line that LOOKS like the compressor marker but is
	// plain content (a read result is never compressor output).
	raw := "+300 more\n" + strings.Repeat("content line\n", 10000) + "+300 more\n"
	var gotResult *ToolResultEvent
	var delivered string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
				delivered = m.Content
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_read", Name: "read", Arguments: `{"path":"notes.txt"}`},
				}, Done: true},
			), nil
		}
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				if strings.Contains(m.Content, " bytes truncated\n") && strings.Contains(m.Content, "+300 more, ") {
					t.Errorf("merged marker fabricated from raw look-like-marker content: %q", m.Content)
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
			Executor: hugeExecutor(map[string]string{"read": raw}),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if gotResult == nil {
		t.Fatal("no ToolResultEvent emitted")
	}
	// The committed tool message keeps the literal "+300 more" content line
	// (byte-dropped only at the head budget, never peeled as a marker) and
	// carries only the plain byte-cap tail.
	if !strings.Contains(delivered, "+300 more\n") {
		t.Errorf("look-like-marker content line was silently stripped from the delivered form: %q", delivered[len(delivered)-60:])
	}
	if strings.Contains(delivered, "+300 more, ") {
		t.Errorf("delivered merged look-like-marker content into a marker: %q", delivered[len(delivered)-60:])
	}
	if !strings.HasSuffix(delivered, " bytes truncated\n") {
		t.Errorf("delivered missing the plain byte-cap tail: %q", delivered[len(delivered)-60:])
	}
}

// TestAgentByteCapWebFetchKeepsExpandPath drives a web_fetch turn whose raw
// page Markdown is huge; the delivered form is capped while the event carries
// the full raw result (the TUI expand seam stays lossless).
func TestAgentByteCapWebFetchKeepsExpandPath(t *testing.T) {
	t.Parallel()
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

	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
}

// hugeExecutor returns a ToolExecutor that serves the canned huge draft for
// the named tools, so the byte-cap tests exercise the engine boundary without
// filesystem/network side effects.
func hugeExecutor(results map[string]string) ToolExecutor {
	return ExecutorFunc(func(_ context.Context, name, _ string) (ToolExecResult, error) {
		if r, ok := results[name]; ok {
			return ToolExecResult{Text: r}, nil
		}
		return ToolExecResult{Text: "result:" + name}, nil
	})
}
