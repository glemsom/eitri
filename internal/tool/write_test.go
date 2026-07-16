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

func TestWrite_Schema(t *testing.T) {
	tool := NewWriteTool("/tmp")
	if tool.Name() != "write" {
		t.Errorf("Name = %q, want 'write'", tool.Name())
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

	// Check path and content are required
	var schemaMap map[string]any
	if err := json.Unmarshal(schema, &schemaMap); err != nil {
		t.Fatal(err)
	}
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	if _, ok := props["path"]; !ok {
		t.Error("schema missing required 'path' property")
	}
	if _, ok := props["content"]; !ok {
		t.Error("schema missing required 'content' property")
	}
}

func TestWrite_InvalidArgs(t *testing.T) {
	tool := NewWriteTool("/tmp")
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestWrite_EmptyPath(t *testing.T) {
	tool := NewWriteTool("/tmp")
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"","content":"hello"}`))
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
	if !strings.Contains(block.Text, "path is required") {
		t.Errorf("expected 'path is required' error, got %q", block.Text)
	}
}

func TestWrite_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"../../etc/passwd","content":"hack"}`))
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
	if !strings.Contains(block.Text, "escapes via") && !strings.Contains(block.Text, "outside workspace") {
		t.Errorf("expected path traversal error, got %q", block.Text)
	}
}

func TestWrite_CreateNewFile(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"sub/hello.txt","content":"hello world"}`))
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

	// File should exist with correct content
	data, err := os.ReadFile(filepath.Join(dir, "sub", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want 'hello world'", string(data))
	}

	// Output should mention the path
	if !strings.Contains(block.Text, "sub/hello.txt") {
		t.Errorf("output should mention file path, got %q", block.Text)
	}
}

func TestWrite_AutoCreateParentDirectories(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/deep.txt","content":"deep content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "d", "deep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep content" {
		t.Errorf("file content = %q, want 'deep content'", string(data))
	}

	// Output should mention the path
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "a/b/c/d/deep.txt") {
		t.Errorf("output should mention file path, got %q", block.Text)
	}
}

func TestWrite_OverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()

	// Create existing file first
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"existing.txt","content":"new content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	// File content should be replaced
	data, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want 'new content'", string(data))
	}

	// Output should mention overwrite
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "overwrite") {
		t.Errorf("output should mention 'overwrite', got %q", block.Text)
	}
}

func TestWrite_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","content":""}`))
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
	if !strings.Contains(block.Text, "content is required") {
		t.Errorf("expected 'content is required' error, got %q", block.Text)
	}
}

func TestWrite_ReportsDirectoriesCreated(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	// Writing to a deeply nested path should report directories created
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/newfile.txt","content":"content"}`))
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

	// Output should mention directories created
	if !strings.Contains(block.Text, "directories") && !strings.Contains(block.Text, "dirs") {
		t.Errorf("output should mention directories created, got %q", block.Text)
	}
}

func TestWrite_NoDirectoriesCreatedOnExistingPath(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "existing")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"existing/file.txt","content":"hello"}`))
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

	// Output should NOT mention directories created (0 dirs)
	if strings.Contains(block.Text, "0 directories") || strings.Contains(block.Text, "0 dirs") {
		t.Errorf("output should not mention zero directories, got %q", block.Text)
	}
}

func TestWrite_PartialDirectoriesCreated(t *testing.T) {
	dir := t.TempDir()
	// Create 'a/b' but not 'a/b/c/d'
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/partial.txt","content":"partial"}`))
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

	// Output should mention directories created
	if !strings.Contains(block.Text, "2 dirs created") {
		t.Errorf("expected '2 dirs created' in output, got %q", block.Text)
	}
}
