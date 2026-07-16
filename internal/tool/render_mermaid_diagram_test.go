package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestRenderMermaidDiagram_Schema(t *testing.T) {
	t.Parallel()
	tool := NewRenderMermaidDiagram()
	if tool.Name() != "render_mermaid_diagram" {
		t.Errorf("Name = %q, want 'render_mermaid_diagram'", tool.Name())
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

func TestRenderMermaidDiagram_ValidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderMermaidDiagram()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"code":"graph TD; A-->B;"}`))
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

func TestRenderMermaidDiagram_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewRenderMermaidDiagram()
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestRenderMermaidDiagram_EmptyCode(t *testing.T) {
	t.Parallel()
	tool := NewRenderMermaidDiagram()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"code":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) > 0 {
		tb, ok := blocks[0].(litellm.TextBlock)
		if ok && tb.Text == "" {
			t.Error("expected error text in result")
		}
	}
}

func TestRenderMermaidDiagram_MissingCode(t *testing.T) {
	t.Parallel()
	tool := NewRenderMermaidDiagram()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true (code is required)")
	}
	_ = blocks
}
