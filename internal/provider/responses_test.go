package provider

import (
	"encoding/json"
	"testing"
)

func TestMarshalResponsesBodyCarriesMultipleSystemMessages(t *testing.T) {
	t.Parallel()
	body, err := marshalResponsesBody(Request{Model: "gpt-5.4-mini", Messages: []Message{
		{Role: RoleSystem, Content: "persona"},
		{Role: RoleSystem, Content: "<available_skills>...</available_skills>"},
		{Role: RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatalf("marshalResponsesBody() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	input, _ := parsed["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input has %d items, want 3 (two system messages + user)", len(input))
	}
	for i := 0; i < 2; i++ {
		item := input[i]
		m, _ := item.(map[string]any)
		if m["role"] != "system" {
			t.Fatalf("input[%d].role = %#v, want system", i, m["role"])
		}
	}
	if tail, _ := input[2].(map[string]any); tail["role"] != "user" {
		t.Fatalf("input[2].role = %#v, want user", tail["role"])
	}
}

func TestOpenAICompletionBodyCarriesMultipleSystemMessages(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(chatCompletionBody{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: RoleSystem, Content: "persona"},
			{Role: RoleSystem, Content: "<available_skills>...</available_skills>"},
			{Role: RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v, want nil", err)
	}
	var parsed struct {
		Messages []map[string]string `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(parsed.Messages) != 3 {
		t.Fatalf("messages has %d items, want 3 (two system messages + user)", len(parsed.Messages))
	}
	for i := 0; i < 2; i++ {
		m := parsed.Messages[i]
		if m["role"] != "system" {
			t.Fatalf("messages[%d].role = %#v, want system", i, m["role"])
		}
	}
	if parsed.Messages[2]["role"] != "user" {
		t.Fatalf("messages[2].role = %#v, want user", parsed.Messages[2]["role"])
	}
}

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
