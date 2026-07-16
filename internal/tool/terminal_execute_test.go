package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/litellm"
)

func TestTerminalExecute_Schema(t *testing.T) {
	tool := NewTerminalExecute(nil)
	if tool.Name() != "terminal_execute" {
		t.Errorf("Name = %q, want 'terminal_execute'", tool.Name())
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

func TestTerminalExecute_InvalidArgs(t *testing.T) {
	tool := NewTerminalExecute(nil)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestTerminalExecute_EmptyCommand(t *testing.T) {
	tool := NewTerminalExecute(nil)
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

func TestTerminalExecute_NilSessionMgr(t *testing.T) {
	tool := NewTerminalExecute(nil)
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

func TestTerminalExecute_NoSessionIDInContext(t *testing.T) {
	// Use a real session manager but context has no session ID
	// We can't easily create a real SessionManager in unit tests,
	// so we test the nil sessionMgr case above for Go error.
	// For the error-tool-result path we need non-nil sessionMgr.
	// This is tested via integration tests.
}

func TestTerminalExecute_ArgsUnmarshal(t *testing.T) {
	// Verify the args struct unmarshals correctly
	args := json.RawMessage(`{"command":"ls -la"}`)
	var parsed terminalExecuteArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Command != "ls -la" {
		t.Errorf("Command = %q, want 'ls -la'", parsed.Command)
	}
}
