package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

func TestAgentBashTurnReturnsCompressedOutput(t *testing.T) {
	t.Parallel()
	var raw strings.Builder
	for i := range 600 {
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

	reg, _ := tools.NewRegistry(tools.Deps{
		Workspace: t.TempDir(),
		TempHost:  t.TempDir(),
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

type fakeBashRunner struct {
	out string
	err error
}

func (f *fakeBashRunner) Run(_ context.Context, _ tools.RunSpec) (*tools.Output, error) {
	return &tools.Output{Stdout: f.out}, f.err
}
