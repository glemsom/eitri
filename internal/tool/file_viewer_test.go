package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/litellm"
)

func TestFileViewer_Schema(t *testing.T) {
	tool := NewFileViewer("/tmp", nil)
	if tool.Name() != "file_viewer" {
		t.Errorf("Name = %q, want 'file_viewer'", tool.Name())
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

func TestFileViewer_InvalidArgs(t *testing.T) {
	tool := NewFileViewer("/tmp", nil)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestFileViewer_EmptyPath(t *testing.T) {
	tool := NewFileViewer("/tmp", nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{}`))
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

func TestFileViewer_ReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello\nworld\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileViewer(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt"}`))
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
	if textBlock.Text != content {
		t.Errorf("content = %q, want %q", textBlock.Text, content)
	}
}

func TestFileViewer_ListDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("b"), 0644)

	tool := NewFileViewer(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":".","mode":"list"}`))
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
		t.Error("expected directory listing text")
	}
}

func TestFileViewer_PathOutsideWorkspace(t *testing.T) {
	tool := NewFileViewer("/tmp/workspace", nil)
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"/etc/passwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestFileViewer_FilePathAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileViewer(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"file_path":"test.txt"}`))
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
	if textBlock.Text != content {
		t.Errorf("content = %q, want %q", textBlock.Text, content)
	}
}

func TestFileViewer_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"path":"dir/file.go","offset":10,"limit":20,"include_line_info":true}`)
	var parsed fileViewerArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Path != "dir/file.go" {
		t.Errorf("Path = %q, want 'dir/file.go'", parsed.Path)
	}
	if parsed.Offset != 10 {
		t.Errorf("Offset = %d, want 10", parsed.Offset)
	}
	if parsed.Limit == nil || *parsed.Limit != 20 {
		t.Errorf("Limit = %v, want 20", parsed.Limit)
	}
	if !parsed.IncludeLineInfo {
		t.Error("IncludeLineInfo = false, want true")
	}
}
