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

// TestDialectManifestChatReExpressesCanonicalTools verifies the Chat-Completions
// dialect folds canonical tool definitions into its function manifest through
// the shared Dialect seam.
func TestDialectManifestChatReExpressesCanonicalTools(t *testing.T) {
	t.Parallel()
	tools := NewChatCompletionsDialect().Manifest(canonicalDefs()).([]Tool)
	if len(tools) != 1 {
		t.Fatalf("Chat manifest = %d tools, want 1", len(tools))
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

// TestDialectManifestChatConsistForCopilot verifies the Copilot chat dialect
// yields the same Chat-Completions function manifest as the shared chat dialect.
func TestDialectManifestChatConsistForCopilot(t *testing.T) {
	t.Parallel()
	chat := NewChatCompletionsDialect().Manifest(canonicalDefs()).([]Tool)
	copilot := NewCopilotChatDialect().Manifest(canonicalDefs()).([]Tool)
	if !reflect.DeepEqual(chat, copilot) {
		t.Fatalf("copilot manifest = %#v, want shared %#v", copilot, chat)
	}
}

// TestDialectManifestResponsesReExpressesCanonicalTools verifies the Responses
// dialect folds canonical tool definitions into its own tool manifest through
// the shared Dialect seam.
func TestDialectManifestResponsesReExpressesCanonicalTools(t *testing.T) {
	t.Parallel()
	manifest := NewResponsesDialect().Manifest(canonicalDefs())
	tools, ok := manifest.([]responsesTool)
	if !ok || len(tools) != 1 {
		t.Fatalf("Responses manifest = %#v, want one responsesTool", manifest)
	}
	got := tools[0]
	want := canonicalDefs()[0].Schema
	if got.Name != "read" || got.Description != "read a file" || got.Type != "function" {
		t.Fatalf("responses tool = %+v", got)
	}
	if !reflect.DeepEqual(got.Parameters, want) {
		t.Fatalf("responses parameters = %#v, want canonical schema %#v", got.Parameters, want)
	}
}
