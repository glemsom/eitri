package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

// TestAgentBashTurnReturnsCompressedOutput drives a bash turn that emits a
// noisy listing through the engine seam and asserts the tool result carried
// back into the conversation is the compressed form: truncated with the
// explicit "+ more" marker, strictly shorter than the raw bytes, and
// deterministic so a re-run recovers the full output (docs/spec.md §5).
func TestAgentBashTurnReturnsCompressedOutput(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 500; i++ {
		raw.WriteString("internal/pkg_")
		raw.WriteString(string(rune('a' + i%26)))
		raw.WriteString(".go   some noisy listing content\n")
	}

	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "list the tree"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls -R ."}`},
				}, Done: true},
			), nil
		}
		// Second round: report what the compressed bash result actually was, so the
		// test can assert on the compressed form from inside the conversation.
		var lastResult string
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == provider.RoleTool {
				lastResult = req.Messages[i].Content
				break
			}
		}
		if !strings.Contains(lastResult, " more") {
			t.Errorf("tool result missing explicit tail marker: %q", lastResult)
		}
		if len(lastResult) >= len(raw.String()) {
			t.Errorf("tool result not compressed (raw=%d, result=%d bytes)", len(raw.String()), len(lastResult))
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done: " + lastResult},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	})

	reg := tools.NewRegistry(tools.Deps{
		Workspace: t.TempDir(),
		TempHost:  t.TempDir(),
		GUID:      tools.GUID("compress-engine"),
		Runner:    &fakeBashRunner{out: raw.String()},
	})

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "list files"}, AgentOptions{
		Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Description: "run shell"}}},
		Executor: executor(reg),
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if !strings.Contains(res.Answer, " more") {
		t.Fatalf("final Answer did not surface compressed output: %q", res.Answer)
	}
}

// fakeBashRunner supplies canned noisy output for the compressor boundary test
// without needing a live bwrap sandbox. It ignores the bwrap argv and returns
// the canned combined output on every invocation.
type fakeBashRunner struct {
	out string
}

func (f *fakeBashRunner) Run(_ context.Context, _ string, _ []string) (*tools.Output, error) {
	return &tools.Output{Stdout: f.out}, nil
}
