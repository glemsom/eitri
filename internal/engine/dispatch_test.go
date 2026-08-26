package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

func scriptedBashCatTurn(t *testing.T) *provider.Scripted {
	t.Helper()
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		switch {
		case toolResults == 0:
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "I'll write a probe"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"echo sandbox-ok > /tmp/probe.txt"}`},
				}, Done: true},
			), nil
		case toolResults == 1:
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "now cat it back"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"cat /tmp/probe.txt"}`},
				}, Done: true},
			), nil
		default:
			lastResult := ""
			for i := len(req.Messages) - 1; i >= 0; i-- {
				if req.Messages[i].Role == provider.RoleTool {
					lastResult = req.Messages[i].Content
					break
				}
			}
			return provider.StreamFunc(
				provider.Chunk{Content: "bash says: " + lastResult},
				provider.Chunk{FinishReason: "stop", Done: true,
					Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 4}},
			), nil
		}
	})
}

func TestDispatchBashThenCatReturnsSandboxOutput(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	workspace := filepath.Join(home, ".eitri-engine-ws")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	th := tools.HostTempFor(tools.GUID("engine"))
	t.Cleanup(func() { _ = os.RemoveAll(th) })
	reg := tools.NewRegistry(tools.Deps{
		Workspace: workspace,
		TempHost:  tools.HostTempFor(tools.GUID("engine")),
		GUID:      tools.GUID("engine"),
		Runner:    tools.RealRunner,
	})

	e := New(scriptedBashCatTurn(t), &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "do it"}, AgentOptions{
		Tools: []provider.Tool{
			{Type: "function", Function: provider.ToolFunction{Name: "bash", Description: "run shell"}},
		},
		Executor: executor(reg),
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if !strings.Contains(res.Answer, "sandbox-ok") {
		t.Fatalf("final Answer = %q, want it to carry sandbox output 'sandbox-ok'", res.Answer)
	}
	if res.Usage == nil || res.Usage.CompletionTokens != 4 {
		t.Fatalf("Usage = %+v, want completion=4", res.Usage)
	}
}

func executor(r *tools.Registry) ToolExecutor {
	return &registryExecutor{r: r}
}

type registryExecutor struct {
	r *tools.Registry
}

func (re *registryExecutor) Execute(ctx context.Context, name, argsJSON string) (ToolExecResult, error) {
	var args map[string]any
	if err := jsonUnmarshal(argsJSON, &args); err != nil {
		return ToolExecResult{}, err
	}
	res, err := re.r.Run(ctx, name, args)
	if err != nil {
		return ToolExecResult{}, err
	}
	return ToolExecResult{Text: res.Text, Compressed: res.Compressed}, nil
}

func jsonUnmarshal(data string, v any) error {
	return json.NewDecoder(strings.NewReader(data)).Decode(v)
}

var _ = json.Valid
