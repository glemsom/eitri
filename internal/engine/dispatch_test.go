package engine

import (
	"context"
	"encoding/json"
	"errors"
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
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"echo sandbox-ok > \"$TMPDIR/probe.txt\""}`},
				}, Done: true},
			), nil
		case toolResults == 1:
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "now cat it back"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"cat \"$TMPDIR/probe.txt\""}`},
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
	workspace := filepath.Join(t.TempDir(), ".eitri-engine-ws")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	tempHost := filepath.Join(t.TempDir(), "tmp")
	reg := tools.NewRegistry(tools.Deps{
		Workspace: workspace,
		TempHost:  tempHost,
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

func TestDispatchPreservesToolOutputOnError(t *testing.T) {
	t.Parallel()
	const listing = "ask-matt\ncodebase-design\ncode-review\n"
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "list the skills"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls ~/.agents/skills/"}`},
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
		return provider.StreamFunc(
			provider.Chunk{Content: "skills: " + lastResult},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	})

	reg := tools.NewRegistry(tools.Deps{
		Workspace: t.TempDir(),
		TempHost:  t.TempDir(),
		GUID:      tools.GUID("dispatch-err"),
		Runner: &fakeBashRunner{out: listing,
			err: errors.New("exit status 2")},
	})

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "list skills"}, AgentOptions{
		Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Description: "run shell"}}},
		Executor: executor(reg),
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if !strings.Contains(res.Answer, listing) {
		t.Fatalf("final Answer did not preserve tool output on error: %q, want it to contain %q", res.Answer, listing)
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
		// Preserve any output the tool produced alongside its error; bash in
		// particular returns combined stdout+stderr even on a non-zero exit, and
		// dropping it would rob the model of diagnostic context (e.g. an ls(1)
		// listing that partially succeeded before failing).
		return ToolExecResult{Text: res.Text, Compressed: res.Compressed}, err
	}
	return ToolExecResult{Text: res.Text, Compressed: res.Compressed}, nil
}

func jsonUnmarshal(data string, v any) error {
	return json.NewDecoder(strings.NewReader(data)).Decode(v)
}

var _ = json.Valid
