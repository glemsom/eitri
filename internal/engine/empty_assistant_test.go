package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func lastUserContent(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}

// A provider stream that ends with EOF before delivering any terminal signal
// (done chunk / finish_reason / tool calls) is a truncated response: the turn
// must surface as a failed run (ErrStreamEOF), never as a silently successful
// empty answer. And it must not persist an empty assistant message to history —
// such a message carries no content, reasoning, or tool calls, and once indexed
// into a later request it becomes an invalid `content: null` assistant block
// that the provider rejects.
func TestRunAgentEOFSilentDoesNotPersistEmptyAssistant(t *testing.T) {
	t.Parallel()

	streams := []int{0, 0}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if lastUserContent(req.Messages) == "continue" {
			for _, m := range req.Messages {
				if m.Role == provider.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
					t.Fatalf("second request carried an empty assistant message: %+v", m)
				}
			}
		}
		if streams[0] == 0 {
			streams[0] = 1
			return provider.StreamFunc(), nil // silent EOF, no output
		}
		streams[1]++
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	const key = "sess-eof-silent"
	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi", SessionKey: key}, AgentOptions{}); !errors.Is(err, ErrStreamEOF) {
		t.Fatalf("first RunAgent error = %v, want truncated-stream error (ErrStreamEOF)", err)
	}

	history := e.sessionHistory(key)
	for _, m := range history {
		if m.Role == provider.RoleAssistant {
			t.Fatalf("history persisted an empty assistant message: %+v", m)
		}
	}

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "continue", SessionKey: key}, AgentOptions{}); err != nil {
		t.Fatalf("second RunAgent error = %v", err)
	}
	if streams[1] == 0 {
		t.Fatal("second provider stream never ran")
	}
}

func TestRunAgentEOFPartialOutputWithoutTerminalSignalFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		chunk provider.Chunk
	}{
		{name: "content", chunk: provider.Chunk{Content: "partial answer"}},
		{name: "reasoning", chunk: provider.Chunk{ReasoningContent: "partial reasoning"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := &mockTranscript{}
			e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
				return provider.StreamFunc(tt.chunk), nil
			}), tr)

			_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{})
			if !errors.Is(err, ErrStreamEOF) {
				t.Fatalf("RunAgent error = %v, want ErrStreamEOF", err)
			}
			if len(tr.lines) != 0 {
				t.Fatalf("transcript lines = %q, want none for truncated output", tr.lines)
			}
		})
	}
}

func TestRunAgentEOFTerminalToolCallSucceeds(t *testing.T) {
	t.Parallel()

	streams := 0
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		streams++
		if streams == 1 {
			return provider.StreamFunc(provider.Chunk{ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: "{}"}}}), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "complete", Done: true}), nil
	}), nil)

	res, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{
		Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		Executor: ExecutorFunc(func(context.Context, string, string) (ToolExecResult, error) { return ToolExecResult{}, nil }),
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if res.Answer != "complete" {
		t.Fatalf("Answer = %q, want complete", res.Answer)
	}
	if streams != 2 {
		t.Fatalf("provider streams = %d, want 2", streams)
	}
}

func TestRunAgentEOFTerminalFinishReasonSucceeds(t *testing.T) {
	t.Parallel()

	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "complete"},
			provider.Chunk{FinishReason: "stop"},
		), nil
	}), nil)

	res, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if res.Answer != "complete" {
		t.Fatalf("Answer = %q, want complete", res.Answer)
	}
}
