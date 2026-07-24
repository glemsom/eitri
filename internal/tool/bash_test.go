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
	if !strings.Contains(tool.Description(), "fresh shell") {
		t.Error("Description should mention fresh-shell semantics")
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
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := NewBashTool("/tmp", 0)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if len(block.Text) == 0 {
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
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	expected := "<stdout>\nhello world\n</stdout>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_ExitCode(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"exit 42"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for non-zero exit")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "[exit code 42]"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_StderrCapture(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo stderr_output >&2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stderr>\nstderr_output\n</stderr>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_StdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo out; echo err >&2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stdout>\nout\n</stdout>\n<stderr>\nerr\n</stderr>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_ExitCodeWithOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello && exit 3"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for non-zero exit")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stdout>\nhello\n</stdout>\n[exit code 3]"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_Timeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Millisecond)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for timeout")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, "[command timed out]") {
		t.Errorf("output = %q, want timed out", block.Text)
	}
}

func TestBash_WorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, dir) {
		t.Errorf("output = %q, want directory %q", block.Text, dir)
	}
}

func TestBash_Truncation(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second)
	// Generate >4 KiB of output
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"python3 -c \"import sys; sys.stdout.write('A' * 6000)\""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if len(block.Text) > 5*1024 {
		t.Errorf("truncated output too long: %d bytes, want <= ~4 KiB", len(block.Text))
	}
	if !strings.HasSuffix(block.Text, "... (output truncated at 4 KiB)") {
		t.Errorf("output should end with truncation marker, got suffix: %q", block.Text[len(block.Text)-50:])
	}
}
