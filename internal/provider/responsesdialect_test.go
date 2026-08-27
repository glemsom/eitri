package provider

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// compile-time proof that the Responses dialect implements the WireDialect seam.
var _ WireDialect = (*ResponsesDialect)(nil)

func TestResponsesDialectCapabilities(t *testing.T) {
	t.Parallel()
	got := NewResponsesDialect().Capabilities()
	want := []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlToolSchemaEnforcement,
		GenerationControlThinkingSuppression,
	}
	if len(got) != len(want) {
		t.Fatalf("Capabilities() = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Capabilities() = %v, want %v", got, want)
		}
	}
}

func TestResponsesDialectBuildShapesControls(t *testing.T) {
	t.Parallel()
	body, err := NewResponsesDialect().Build(Request{
		Model:                 "gpt-5.4-mini",
		Messages:              []Message{{Role: RoleUser, Content: "hi"}},
		Tools:                 strictToolList(),
		ThinkingEnabled:       true,
		ReasoningEffort:       "high",
		MaxOutputTokens:       256,
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	checks := map[string]any{
		"model":             "gpt-5.4-mini",
		"stream":            true,
		"max_output_tokens": float64(256),
		"reasoning":         map[string]any{"effort": "high"},
	}
	for key, want := range checks {
		if got, ok := parsed[key]; !ok || !jsonEqual(got, want) {
			t.Errorf("%q = %#v, want %#v", key, parsed[key], want)
		}
	}
	tools, ok := parsed["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("tools = %#v, want two strict tools", parsed["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["strict"] != true {
		t.Fatalf("tool = %#v, want strict:true when enforcement is on", tool)
	}
}

func TestResponsesDialectBuildOmitsUnsetControls(t *testing.T) {
	t.Parallel()
	body, err := NewResponsesDialect().Build(Request{
		Model:    "gpt-5.4-mini",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	for _, absent := range []string{`"reasoning"`, `"max_output_tokens"`, `"tools"`, `"tool_choice"`} {
		if strings.Contains(string(body), absent) {
			t.Errorf("unset control %s leaked into body: %s", absent, body)
		}
	}
}

func TestResponsesDialectStreamReassemblesToolCallsAndText(t *testing.T) {
	t.Parallel()
	sse := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hello "}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"read"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"x\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"read"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_tool","model":"gpt-5.4-mini","created_at":2,"usage":{"input_tokens":5,"output_tokens":1},"output":[{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"x\"}"}]}}`,
		`data: [DONE]`,
	}, "\n\n\n")

	stream := NewResponsesDialect().Stream(strings.NewReader(sse + "\n\n\n"))
	var text string
	var last Chunk
	for {
		ch, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		if ch.Done {
			last = ch
		}
		text += ch.Content
	}
	if text != "Hello " {
		t.Fatalf("streamed text = %q, want %q", text, "Hello ")
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("terminal chunk ToolCalls = %+v, want one reassembled call", last.ToolCalls)
	}
	tc := last.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "read" || tc.Arguments != `{"path":"x"}` {
		t.Fatalf("tool call not reassembled: %+v", tc)
	}
}
