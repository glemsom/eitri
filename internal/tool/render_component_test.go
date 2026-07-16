package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestRenderComponent_Schema(t *testing.T) {
	tool := NewRenderComponent()
	if tool.Name() != "render_component" {
		t.Errorf("Name = %q, want 'render_component'", tool.Name())
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

func TestRenderComponent_InvalidArgs(t *testing.T) {
	tool := NewRenderComponent()
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestRenderComponent_EmptyName(t *testing.T) {
	tool := NewRenderComponent()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) > 0 {
		result, ok := blocks[0].(litellm.TextBlock)
		if ok && result.Text == "" {
			t.Error("expected error text")
		}
	}
}

func TestRenderComponent_MermaidDiagram(t *testing.T) {
	tool := NewRenderComponent()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"MermaidDiagram","data":{"code":"graph TD; A-->B;"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	textBlock, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if len(textBlock.Text) == 0 {
		t.Error("expected result text")
	}
}

func TestRenderComponent_MermaidNoCode(t *testing.T) {
	tool := NewRenderComponent()
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"MermaidDiagram","data":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestRenderComponent_QuickReplies(t *testing.T) {
	tool := NewRenderComponent()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"QuickReplies","data":{"options":["yes","no"]}}`))
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

func TestRenderComponent_QuickRepliesEmpty(t *testing.T) {
	tool := NewRenderComponent()
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"QuickReplies","data":{"options":[]}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestRenderComponent_DiffCard(t *testing.T) {
	tool := NewRenderComponent()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"DiffCard","data":{"old":"hello","new":"world"}}`))
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

func TestRenderComponent_DiffCardMissingFields(t *testing.T) {
	tool := NewRenderComponent()
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"DiffCard","data":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestRenderComponent_UnknownComponent(t *testing.T) {
	tool := NewRenderComponent()
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"UnknownComp","data":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestRenderComponent_NilDataWithValidName(t *testing.T) {
	tool := NewRenderComponent()
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"name":"MermaidDiagram"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true (Mermaid needs data.code)")
	}
}

func TestRenderComponent_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"name":"MermaidDiagram","data":{"code":"graph TD; A-->B;"}}`)
	var parsed renderComponentArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Name != "MermaidDiagram" {
		t.Errorf("Name = %q, want 'MermaidDiagram'", parsed.Name)
	}
	if parsed.Data == nil {
		t.Fatal("Data is nil")
	}
	code, ok := parsed.Data["code"]
	if !ok {
		t.Fatal("data.code not found")
	}
	if code != "graph TD; A-->B;" {
		t.Errorf("code = %v, want 'graph TD; A-->B;'", code)
	}
}
