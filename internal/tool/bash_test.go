package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/voocel/litellm"
)

func TestBash_Schema(t *testing.T) {
	tool := NewBashTool("/tmp", 0)
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
	tool := NewBashTool("/tmp", 0)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := NewBashTool("/tmp", 0)
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

func TestBash_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"command":"ls -la"}`)
	var parsed bashArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Command != "ls -la" {
		t.Errorf("Command = %q, want 'ls -la'", parsed.Command)
	}
}

func TestBash_RunsCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	result, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(result.Text, "hello world") {
		t.Errorf("output = %q, want 'hello world'", result.Text)
	}
}

func TestBash_ExitCode(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"exit 42"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for non-zero exit")
	}
	result := blocks[0].(litellm.TextBlock)
	if !strings.Contains(result.Text, "[exit code 42]") {
		t.Errorf("output = %q, want exit code 42", result.Text)
	}
}

func TestBash_StderrCapture(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"echo stderr_output >&2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	result := blocks[0].(litellm.TextBlock)
	if !strings.Contains(result.Text, "stderr_output") {
		t.Errorf("output = %q, want stderr_output", result.Text)
	}
}

func TestBash_Timeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Millisecond)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for timeout")
	}
	result := blocks[0].(litellm.TextBlock)
	if !strings.Contains(result.Text, "[command timed out]") {
		t.Errorf("output = %q, want timed out", result.Text)
	}
}

func TestBash_WorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	result := blocks[0].(litellm.TextBlock)
	if !strings.Contains(result.Text, dir) {
		t.Errorf("output = %q, want directory %q", result.Text, dir)
	}
}
