package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestRenderDiffCard_Schema(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	if tool.Name() != "render_diff_card" {
		t.Errorf("Name = %q, want 'render_diff_card'", tool.Name())
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

func TestRenderDiffCard_ValidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"old":"hello","new":"world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", blocks[0])
	}
	if tb.Text == "" {
		t.Error("expected non-empty result text")
	}
}

func TestRenderDiffCard_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestRenderDiffCard_BothEmpty(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"old":"","new":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true (at least one non-empty)")
	}
	if len(blocks) > 0 {
		tb, ok := blocks[0].(litellm.TextBlock)
		if ok && tb.Text == "" {
			t.Error("expected error text in result")
		}
	}
}

func TestRenderDiffCard_WithLang(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"old":"foo","new":"bar","lang":"go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
}

func TestRenderDiffCard_OnlyOld(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"old":"content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false (only old is fine)")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
}

func TestRenderDiffCard_OnlyNew(t *testing.T) {
	t.Parallel()
	tool := NewRenderDiffCard()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"new":"content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false (only new is fine)")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
}
