package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func strictToolDefs() []provider.Tool {
	return []provider.Tool{
		{Type: "function", Function: provider.ToolFunction{
			Name:        "bash",
			Description: "run a shell command",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []any{"command"},
			},
		}},
		{Type: "function", Function: provider.ToolFunction{
			Name:        "read",
			Description: "read a file",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"path":       map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "integer"},
					"end_line":   map[string]any{"type": "integer"},
				},
				"required": []any{"path"},
			},
		}},
	}
}

type mockToolRecorder struct {
	calls []callRecord
}

type callRecord struct {
	name string
	args map[string]any
}

func (m *mockToolRecorder) Execute(_ context.Context, name, argsJSON string) (ToolExecResult, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolExecResult{}, err
	}
	m.calls = append(m.calls, callRecord{name: name, args: args})
	return ToolExecResult{Text: "result:" + name}, nil
}

func toolResultContents(messages []provider.Message) []string {
	var out []string
	for _, m := range messages {
		if m.Role == provider.RoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestInvalidArgumentsReturnWrappedINVALIDJSON(t *testing.T) {
	t.Parallel()
	var lastResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		lastResults = toolResultContents(req.Messages)
		if len(lastResults) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bad", Type: "function", Name: "bash", Arguments: `{"command": "echo ", oops`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "acknowledged the bad args"},
			provider.Chunk{FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 6}},
		), nil
	})

	rec := &mockToolRecorder{}
	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "run bash"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: rec,
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "acknowledged the bad args" {
		t.Fatalf("Answer = %q, want recovery answer", res.Answer)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("executor called with %+v, want no calls for malformed args", rec.calls)
	}
	if len(lastResults) != 1 {
		t.Fatalf("tool-results resubmitted = %d, want 1 INVALID_JSON result", len(lastResults))
	}
	var wrapped map[string]string
	if err := json.Unmarshal([]byte(lastResults[0]), &wrapped); err != nil {
		t.Fatalf("tool result %q is not valid JSON: %v", lastResults[0], err)
	}
	if got, ok := wrapped["INVALID_JSON"]; !ok || got != `{"command": "echo ", oops` {
		t.Fatalf("INVALID_JSON value = %q, want the raw unparseable arguments", got)
	}
}

func TestParallelToolCallsDispatchAllInOnePass(t *testing.T) {
	t.Parallel()
	var lastIDs []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolCount int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolCount++
				lastIDs = append(lastIDs, m.ToolCallID)
			}
		}
		if toolCount == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_a", Type: "function", Name: "bash", Arguments: `{"command":"echo a"}`},
					{ID: "call_b", Type: "function", Name: "read", Arguments: `{"path":"f.txt","start_line":null,"end_line":null}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "did both"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})

	rec := &mockToolRecorder{}
	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: rec,
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "did both" {
		t.Fatalf("Answer = %q, want recovery answer", res.Answer)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(rec.calls))
	}
	if rec.calls[0].name != "bash" || rec.calls[1].name != "read" {
		t.Fatalf("call order = %+v, want bash then read", rec.calls)
	}
	if len(lastIDs) != 2 || lastIDs[0] != "call_a" || lastIDs[1] != "call_b" {
		t.Fatalf("resubmitted tool_call_ids = %v, want [call_a call_b]", lastIDs)
	}
}

func TestStrictShapeRejectsSchemaViolatingCall(t *testing.T) {
	t.Parallel()
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bad", Type: "function", Name: "bash", Arguments: `{"typo":"echo hi"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "fixed"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})

	rec := &mockToolRecorder{}
	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: rec,
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "fixed" {
		t.Fatalf("Answer = %q, want recovery answer", res.Answer)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("executor called with %+v, want no calls for a schema-violating call", rec.calls)
	}
}

func TestRequiredSubsetReadValidates(t *testing.T) {
	t.Parallel()
	for _, args := range []string{
		`{"path":"f.txt"}`,
		`{"path":"f.txt","start_line":12,"end_line":340}`,
		`{"path":"f.txt","start_line":null,"end_line":null}`,
	} {
		if err := validateToolCallArgs(strictToolDefs()[1].Function.Parameters, args, nil); err != nil {
			t.Fatalf("validateToolCallArgs(%s) error = %v, want nil", args, err)
		}
	}
}

func TestNullableUnionToleratesNull(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":       map[string]any{"type": "string"},
			"start_line": []any{"integer", "null"},
		},
		"required": []string{"path"},
	}
	for _, args := range []string{
		`{"path":"f.txt","start_line":null}`,
		`{"path":"f.txt","start_line":12}`,
	} {
		if err := validateToolCallArgs(schema, args, nil); err != nil {
			t.Fatalf("validateToolCallArgs(%s) error = %v, want nil for nullable union of null value", args, err)
		}
	}
}

func TestRunAgentRejectsUndeclaredToolCall(t *testing.T) {
	t.Parallel()
	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResultContents(req.Messages)
		if len(toolResults) == 0 {
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_unknown", Name: "write", Arguments: `{}`},
			}, Done: true}), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "recovered", FinishReason: "stop", Done: true}), nil
	})
	rec := &mockToolRecorder{}
	e := New(scripted, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "model", Prompt: "go"}, AgentOptions{
		Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		Executor: rec,
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("executor calls = %+v, want none", rec.calls)
	}
	if len(toolResults) != 1 || !contains(toolResults[0], `undeclared tool "write"`) {
		t.Fatalf("tool results = %q, want undeclared-tool error", toolResults)
	}
}

func TestRunAgentExecutesDeclaredSchemaLessTool(t *testing.T) {
	t.Parallel()
	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResultContents(req.Messages)
		if len(toolResults) == 0 {
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_bash", Name: "bash", Arguments: `{}`},
			}, Done: true}), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	})
	rec := &mockToolRecorder{}
	e := New(scripted, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "model", Prompt: "go"}, AgentOptions{
		Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		Executor: rec,
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].name != "bash" {
		t.Fatalf("executor calls = %+v, want one call to declared schema-less tool bash", rec.calls)
	}
	if len(toolResults) != 1 || toolResults[0] != "result:bash" {
		t.Fatalf("tool results = %q, want schema-less tool result", toolResults)
	}
}
