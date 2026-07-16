package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestGrep_Schema(t *testing.T) {
	tool := NewGrepTool("/tmp")
	if tool.Name() != "grep" {
		t.Errorf("Name = %q, want 'grep'", tool.Name())
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

func TestGrep_InvalidArgs(t *testing.T) {
	tool := NewGrepTool("/tmp")
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestGrep_EmptyPattern(t *testing.T) {
	tool := NewGrepTool("/tmp")
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "required") {
		t.Errorf("expected error mentioning 'required', got %q", block.Text)
	}
}

func TestGrep_SuccessfulSearch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"foo.go":      "package main\n\nfunc foo() {}\n",
		"bar.go":      "package main\n\nfunc bar() {}\nconst foo = 42\n",
		"sub/qux.go":  "package qux\n\nfunc qux() {}\n",
		"readme.txt":  "This file contains foo\n",
	}
	for path, content := range files {
		fp := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"foo"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}

	lines := strings.Split(strings.TrimSpace(block.Text), "\n")
	// Should match: foo.go (line 3 "func foo() {}"), bar.go (line 4 "const foo = 42"), readme.txt (line 1 "This file contains foo")
	// sub/qux.go has no "foo"
	if len(lines) != 3 {
		t.Errorf("expected 3 matches, got %d: %v", len(lines), lines)
	}

	// Check format: file:line:content
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			t.Errorf("expected file:line:content format, got %q", line)
		}
	}
}

func TestGrep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"nonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) > 0 {
		block, ok := blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", blocks[0])
		}
		if block.Text != "" {
			t.Errorf("expected empty output, got %q", block.Text)
		}
	}
}

func TestGrep_FilePatternFilter(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"foo.go":    "package main\nfunc hello() {}\n",
		"bar.txt":   "hello world\n",
		"sub/baz.go": "package sub\nfunc hello() {}\n",
	}
	for path, content := range files {
		fp := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(dir)
	// Search for "hello" only in *.go files
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"hello","file_pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}

	lines := strings.Split(strings.TrimSpace(block.Text), "\n")
	// Should match foo.go and sub/baz.go, but NOT bar.txt
	if len(lines) != 2 {
		t.Errorf("expected 2 matches (only .go files), got %d: %v", len(lines), lines)
	}
}

func TestGrep_OutputTruncation(t *testing.T) {
	dir := t.TempDir()
	// Create a file with enough lines to exceed 128 KiB output
	var content strings.Builder
	for i := 0; i < 10000; i++ {
		content.WriteString("this is a test line that contains the word match somewhere in it\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"match"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}

	t.Logf("output size: %d bytes", len(block.Text))

	// Output should be capped at 128 KiB, and should have a truncation marker
	maxSize := 128 * 1024
	if len(block.Text) > maxSize {
		t.Errorf("output size = %d, want <= %d", len(block.Text), maxSize)
	}
	if !strings.Contains(block.Text, "... truncated") {
		t.Error("expected truncation marker '... truncated' in output")
	}
}

func TestGrep_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"pattern":"foo","file_pattern":"*.go"}`)
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Pattern != "foo" {
		t.Errorf("Pattern = %q, want 'foo'", parsed.Pattern)
	}
	if parsed.FilePattern == nil || *parsed.FilePattern != "*.go" {
		t.Errorf("FilePattern = %v, want '*.go'", parsed.FilePattern)
	}
}

func TestGrep_FilePatternOptional(t *testing.T) {
	args := json.RawMessage(`{"pattern":"test"}`)
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Pattern != "test" {
		t.Errorf("Pattern = %q, want 'test'", parsed.Pattern)
	}
	if parsed.FilePattern != nil {
		t.Errorf("FilePattern = %v, want nil (optional)", *parsed.FilePattern)
	}
}
