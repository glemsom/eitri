package provider

import (
	"encoding/json"
	"testing"
)

func TestMarshalResponsesBodyKeepsEmptyFunctionCallOutput(t *testing.T) {
	t.Parallel()
	body, err := marshalResponsesBody(Request{Model: "gpt-5.4-mini", Messages: []Message{
		{Role: RoleUser, Content: "read file"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Name: "read", Arguments: `{"path":"empty.txt"}`}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: ""},
	}})
	if err != nil {
		t.Fatalf("marshalResponsesBody() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	input, ok := parsed["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input = %#v, want 3 items", parsed["input"])
	}
	toolOut, ok := input[2].(map[string]any)
	if !ok {
		t.Fatalf("input[2] = %#v, want object", input[2])
	}
	if toolOut["type"] != "function_call_output" {
		t.Fatalf("input[2].type = %#v, want function_call_output", toolOut["type"])
	}
	if _, ok := toolOut["output"]; !ok {
		t.Fatalf("input[2] = %#v, missing required output field", toolOut)
	}
	if got, _ := toolOut["output"].(string); got != "" {
		t.Fatalf("input[2].output = %q, want empty string preserved", got)
	}
}
