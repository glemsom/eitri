package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// strictToolDefs returns the canonical Chat-Completions tool manifest for the
// tools T5's dispatch tests exercise: bash, read, write. Schemas are
// strict-shaped (additionalProperties:false); read requires only path, with
// nullable unions for the line-range optionals, so schema-validation tests
// target the real shape.
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
					"start_line": []any{"integer", "null"},
					"end_line":   []any{"integer", "null"},
				},
				"required": []any{"path"},
			},
		}},
	}
}

// mockToolRecorder is a ToolExecutor that records the calls it received so a
// test can assert exactly which tool calls the dispatch loop executed, in
// order, with their parsed arguments.
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

// toolCallMessage returns the list of role:"tool" message contents present on
// the most recent run (used by scripted providers to feed the model history
// back into later turns).
func toolResultContents(messages []provider.Message) []string {
	var out []string
	for _, m := range messages {
		if m.Role == provider.RoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

// TestInvalidArgumentsReturnWrappedINVALIDJSON drives a tool turn whose first
// call carries malformed/truncated JSON arguments. The engine must not crash
// and must not silently skip: it returns {"INVALID_JSON": "<raw>"} (built via
// a JSON library, so escaping stays correct) as the role:"tool" result and
// continues the loop until the model answers. This is the acceptance
// criterion for malformed/truncated-arg recovery.
func TestInvalidArgumentsReturnWrappedINVALIDJSON(t *testing.T) {
	var lastResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		lastResults = toolResultContents(req.Messages)
		if len(lastResults) == 0 {
			// First turn: request a bash call whose arguments cannot be parsed.
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bad", Type: "function", Name: "bash", Arguments: `{"command": "echo ", oops`},
				}, Done: true},
			), nil
		}
		// Second turn: the model acknowledged the error and answered.
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
	// The malformed call must never reach the executor.
	if len(rec.calls) != 0 {
		t.Fatalf("executor called with %+v, want no calls for malformed args", rec.calls)
	}
	if len(lastResults) != 1 {
		t.Fatalf("tool-results resubmitted = %d, want 1 INVALID_JSON result", len(lastResults))
	}
	// The wrapper must be a valid JSON object {"INVALID_JSON": "<raw>"} built by
	// the JSON library so quotes/escapes in the raw fragment are preserved.
	var wrapped map[string]string
	if err := json.Unmarshal([]byte(lastResults[0]), &wrapped); err != nil {
		t.Fatalf("tool result %q is not valid JSON: %v", lastResults[0], err)
	}
	if got, ok := wrapped["INVALID_JSON"]; !ok || got != `{"command": "echo ", oops` {
		t.Fatalf("INVALID_JSON value = %q, want the raw unparseable arguments", got)
	}
}

// TestParallelToolCallsDispatchAllInOnePass drives a single turn that returns
// two tool calls in parallel (bash + read). The engine must execute both in
// one pass and append a role:"tool" result per call with the matching
// tool_call_id, never assuming a singleton. This is the acceptance criterion
// for multi-call dispatch.
func TestParallelToolCallsDispatchAllInOnePass(t *testing.T) {
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
	// Both parallel calls executed once each, correct order preserved.
	if len(rec.calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(rec.calls))
	}
	if rec.calls[0].name != "bash" || rec.calls[1].name != "read" {
		t.Fatalf("call order = %+v, want bash then read", rec.calls)
	}
	// Two role:"tool" results resubmitted, each with its own tool_call_id.
	if len(lastIDs) != 2 || lastIDs[0] != "call_a" || lastIDs[1] != "call_b" {
		t.Fatalf("resubmitted tool_call_ids = %v, want [call_a call_b]", lastIDs)
	}
}

// TestStrictShapeRejectsSchemaViolatingCall drives a tool call that violates
// the strict schema (a missing required field under additionalProperties:false).
// The engine must reject it with a schema-error tool result and continue the
// loop, rather than executing or crashing.
func TestStrictShapeRejectsSchemaViolatingCall(t *testing.T) {
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			// bash requires "command"; the call omits it.
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

// TestRequiredSubsetReadValidates verifies a read call carrying only the
// genuinely-required field (path) — no null placeholders for the optional
// line range — passes schema validation under the strict-shape validator.
func TestRequiredSubsetReadValidates(t *testing.T) {
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

// TestNullableUnionToleratesNullVerifies the strict-shape validator accepts a
// nullable-union field set to null (how a model emits an omitted optional).
func TestNullableUnionToleratesNull(t *testing.T) {
	err := validateToolCallArgs(strictToolDefs()[1].Function.Parameters, `{"path":"f.txt","start_line":null,"end_line":null}`, nil)
	if err != nil {
		t.Fatalf("validateToolCallArgs() error = %v, want nil for nullable union of null value", err)
	}
}
