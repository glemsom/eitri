package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/litellm"

	eitriagent "github.com/glemsom/eitri/internal/agent"
)

func TestFileEditor_Schema(t *testing.T) {
	tool := NewFileEditor("/tmp")
	if tool.Name() != "file_editor" {
		t.Errorf("Name = %q, want 'file_editor'", tool.Name())
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

func TestFileEditor_InvalidArgs(t *testing.T) {
	tool := NewFileEditor("/tmp")
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestFileEditor_EmptyPath(t *testing.T) {
	tool := NewFileEditor("/tmp")
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"create"}`))
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

func TestFileEditor_CreateFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewFileEditor(dir)

	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"create","path":"newfile.txt","content":"hello world"}`))
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

	// Verify file was created
	data, err := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want 'hello world'", string(data))
	}
}

func TestFileEditor_OverwriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditor(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"overwrite","path":"existing.txt","content":"new content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Errorf("content = %q, want 'new content'", string(data))
	}
}

func TestFileEditor_EditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "editable.txt")
	content := "hello world\nfoo bar\nbaz qux\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditor(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"edit","path":"editable.txt","old":"foo bar","new":"FOO BAR"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "hello world\nFOO BAR\nbaz qux\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestFileEditor_EditWithMissingOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditor(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"edit","path":"test.txt","new":"new"}`))
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

func TestFileEditor_PathOutsideWorkspace(t *testing.T) {
	tool := NewFileEditor("/tmp/workspace")
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"create","path":"/etc/outside.txt","content":"hack"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true (path outside workspace)")
	}
}

func TestFileEditor_InsertLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Need the anchor from line2's hash
	hash := eitriagent.LineHash("line2")
	anchor := "2:" + hash

	tool := NewFileEditor(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"insert","path":"test.txt","anchor":"`+anchor+`","content":"INSERTED"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\nINSERTED\nline3\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestFileEditor_CreateWithParentDirs(t *testing.T) {
	dir := t.TempDir()
	tool := NewFileEditor(dir)

	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"mode":"create","path":"sub/nested/newfile.txt","content":"nested"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}

	data, err := os.ReadFile(filepath.Join(dir, "sub", "nested", "newfile.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested" {
		t.Errorf("content = %q, want 'nested'", string(data))
	}
}

func TestFileEditor_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"path":"file.go","mode":"edit","old":"foo","new":"bar","anchor":"15:a1b2c3","content":""}`)
	var parsed fileEditorArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Path != "file.go" {
		t.Errorf("Path = %q, want 'file.go'", parsed.Path)
	}
	if parsed.Mode != "edit" {
		t.Errorf("Mode = %q, want 'edit'", parsed.Mode)
	}
	if parsed.Old != "foo" {
		t.Errorf("Old = %q, want 'foo'", parsed.Old)
	}
	if parsed.New != "bar" {
		t.Errorf("New = %q, want 'bar'", parsed.New)
	}
	if parsed.Anchor != "15:a1b2c3" {
		t.Errorf("Anchor = %q, want '15:a1b2c3'", parsed.Anchor)
	}
}
