package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestRenderQuickReplies_Schema(t *testing.T) {
	t.Parallel()
	tool := NewRenderQuickReplies()
	if tool.Name() != "render_quick_replies" {
		t.Errorf("Name = %q, want 'render_quick_replies'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestRenderQuickReplies_ValidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderQuickReplies()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"options":["yes","no","maybe"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", result.Blocks[0])
	}
	if tb.Text == "" {
		t.Error("expected non-empty result text")
	}
}

func TestRenderQuickReplies_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderQuickReplies()
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestRenderQuickReplies_EmptyOptions(t *testing.T) {
	t.Parallel()
	tool := NewRenderQuickReplies()
	result, err := tool.Call(context.Background(), json.RawMessage(`{"options":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) > 0 {
		tb, ok := result.Blocks[0].(litellm.TextBlock)
		if ok && tb.Text == "" {
			t.Error("expected error text in result")
		}
	}
}

func TestRenderQuickReplies_MissingOptions(t *testing.T) {
	t.Parallel()
	tool := NewRenderQuickReplies()
	result, err := tool.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true (options is required)")
	}
}
