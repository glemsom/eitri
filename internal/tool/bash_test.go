package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestBash_Schema(t *testing.T) {
	tool := NewBashTool(nil)
	if tool.Name() != "bash" {
		t.Errorf("Name = %q, want 'bash'", tool.Name())
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

func TestBash_InvalidArgs(t *testing.T) {
	tool := NewBashTool(nil)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := NewBashTool(nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	result, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if len(result.Text) == 0 {
		t.Error("expected error text")
	}
}

func TestBash_NilSessionMgr(t *testing.T) {
	tool := NewBashTool(nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
}

func TestBash_ArgsUnmarshal(t *testing.T) {
	// Verify the args struct unmarshals correctly
	args := json.RawMessage(`{"command":"ls -la"}`)
	var parsed bashArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Command != "ls -la" {
		t.Errorf("Command = %q, want 'ls -la'", parsed.Command)
	}
}
