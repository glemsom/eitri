package provider

import (
	"encoding/json"
	"testing"
)

func TestAnthropicBuildOmitsEmptyAssistantMessage(t *testing.T) {
	data, err := NewAnthropicDialect().Build(Request{
		Model: "union-alpha",
		Messages: []Message{
			{Role: RoleUser, Content: "Review the architecture"},
			{Role: RoleAssistant},
			{Role: RoleUser, Content: "Continue"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	for i, message := range body.Messages {
		if string(message.Content) == "null" || len(message.Content) == 0 {
			t.Fatalf("messages[%d].content must be a string or an array of content blocks; got %s", i, message.Content)
		}
	}
	if len(body.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (empty assistant omitted)", len(body.Messages))
	}
}
