package provider

import (
	"reflect"
	"testing"
)

// canonicalDefs is one canonical schema set shared across both dialects, the
// single source of truth that must not be duplicated per transport.
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

// TestReExpressChatWrapsParameters verifies the canonical schema is emitted
// under function.parameters for the Chat Completions dialect.
func TestReExpressChatWrapsParameters(t *testing.T) {
	tools := ReExpress(canonicalDefs(), DialectChat).([]Tool)
	if len(tools) != 1 {
		t.Fatalf("Chat tools = %d, want 1", len(tools))
	}
	got := tools[0]
	want := canonicalDefs()[0].Schema
	// The exact same schema map instance must be used, not a copy: the strict
	// shape is shared, never re-authored per dialect.
	if got.Function.Name != "read" || got.Function.Description != "read a file" {
		t.Fatalf("chat tool = %+v", got)
	}
	if !reflect.DeepEqual(got.Function.Parameters, want) {
		t.Fatalf("chat parameters = %#v, want canonical schema %#v", got.Function.Parameters, want)
	}
}

// TestReExpressAnthropicWrapsInputSchema verifies the same canonical schema is
// emitted under input_schema (with a strict flag) for the Anthropic dialect.
func TestReExpressAnthropicWrapsInputSchema(t *testing.T) {
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
	// The two dialects must produce an identical schema surface so strict mode
	// is valid across transports.
	chat := ReExpress(canonicalDefs(), DialectChat).([]Tool)[0].Function.Parameters
	if !reflect.DeepEqual(chat, got.InputSchema) {
		t.Fatalf("dialect schemas diverged: chat %#v vs anthropic %#v", chat, got.InputSchema)
	}
}
