package provider

import (
	"reflect"
	"testing"
)

func canonicalDefs() []DialectDefinition {
	readSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	return []DialectDefinition{
		{Name: "read", Description: "read a file", Schema: readSchema},
	}
}

func TestReExpressChatWrapsParameters(t *testing.T) {
	t.Parallel()
	tools := ReExpress(canonicalDefs(), DialectChat).([]Tool)
	if len(tools) != 1 {
		t.Fatalf("Chat tools = %d, want 1", len(tools))
	}
	got := tools[0]
	want := canonicalDefs()[0].Schema
	if got.Function.Name != "read" || got.Function.Description != "read a file" {
		t.Fatalf("chat tool = %+v", got)
	}
	if !reflect.DeepEqual(got.Function.Parameters, want) {
		t.Fatalf("chat parameters = %#v, want canonical schema %#v", got.Function.Parameters, want)
	}
}

func TestReExpressAnthropicWrapsInputSchema(t *testing.T) {
	t.Parallel()
	tools := ReExpress(canonicalDefs(), DialectAnthropic).([]AnthropicTool)
	if len(tools) != 1 {
		t.Fatalf("Anthropic tools = %d, want 1", len(tools))
	}
	got := tools[0]
	want := canonicalDefs()[0].Schema
	if got.Name != "read" || got.Description != "read a file" {
		t.Fatalf("anthropic tool = %+v", got)
	}
	if !reflect.DeepEqual(got.InputSchema, want) {
		t.Fatalf("anthropic input_schema = %#v, want canonical schema %#v", got.InputSchema, want)
	}
	chat := ReExpress(canonicalDefs(), DialectChat).([]Tool)[0].Function.Parameters
	if !reflect.DeepEqual(chat, got.InputSchema) {
		t.Fatalf("dialect schemas diverged: chat %#v vs anthropic %#v", chat, got.InputSchema)
	}
}
