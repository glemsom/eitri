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

var mergedMarkerRe = regexp.MustCompile(`\+[0-9]+ more, \+[0-9]+ bytes truncated\n$`)

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
	}
}

func hugeDraft(prefix string) string {
	return strings.Repeat(prefix+" payload line number "+
		strings.Repeat("x", 40)+"\n", 8000) // ~450 KiB
}

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
	if gotResult.Result != raw {
		t.Errorf("ToolResultEvent.Result is not the full raw result (len=%d, want %d)",
			len(gotResult.Result), len(raw))
	}
	if gotResult.BytesDropped <= 0 {
		t.Errorf("ToolResultEvent.BytesDropped = %d, want > 0", gotResult.BytesDropped)
	}
}

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
					return ToolExecResult{Text: draft, Compressed: true, Dropped: 29900}, nil
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
	if gotResult.Result != draft {
		t.Errorf("ToolResultEvent.Result is not the full pre-cap draft (len=%d, want %d)",
			len(gotResult.Result), len(draft))
	}
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

func TestAgentByteCapPreservesLookLikeMarkerContent(t *testing.T) {
	t.Parallel()
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
	if gotResult.Compressed || gotResult.Dropped != 0 {
		t.Errorf("marker-like tool content reported compression metadata: %+v", *gotResult)
	}
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

func hugeExecutor(results map[string]string) ToolExecutor {
	return ExecutorFunc(func(_ context.Context, name, _ string) (ToolExecResult, error) {
		if r, ok := results[name]; ok {
			return ToolExecResult{Text: r}, nil
		}
		return ToolExecResult{Text: "result:" + name}, nil
	})
}
