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

func TestEdit_Schema(t *testing.T) {
	tool := NewEditTool("/tmp/workspace")
	if tool.Name() != "edit" {
		t.Errorf("Name = %q, want 'edit'", tool.Name())
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

func TestEdit_InvalidArgs(t *testing.T) {
	tool := NewEditTool("/tmp/workspace")
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestEdit_EmptyPath(t *testing.T) {
	tool := NewEditTool("/tmp/workspace")
	result, err := tool.Call(context.Background(), json.RawMessage(`{"old_text":"foo","new_text":"bar"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) > 0 {
		result, ok := result.Blocks[0].(litellm.TextBlock)
		if ok && result.Text == "" {
			t.Error("expected error text")
		}
	}
}

func TestEdit_SuccessfulEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello world\nfoo bar\nbaz qux\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","old_text":"foo bar","new_text":"FOO BAR"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}

	// Verify file was changed on disk
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "hello world\nFOO BAR\nbaz qux\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestEdit_NoMatch_IncludesContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line one\nline two\nline three\nline four\nline five\nline six\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","old_text":"nonexistent","new_text":"replacement"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	textBlock, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(textBlock.Text, "not found") {
		t.Errorf("error text should contain 'not found', got: %q", textBlock.Text)
	}
	if !strings.Contains(textBlock.Text, "File starts with:") {
		t.Errorf("error text should contain 'File starts with:' context hint, got: %q", textBlock.Text)
	}
	if !strings.Contains(textBlock.Text, "line one") {
		t.Errorf("error text should contain first line of file, got: %q", textBlock.Text)
	}
	if !strings.Contains(textBlock.Text, "line five") {
		t.Errorf("error text should contain up to 5 lines of file, got: %q", textBlock.Text)
	}
	if strings.Contains(textBlock.Text, "line six") {
		t.Errorf("error text should not show more than 5 lines, got: %q", textBlock.Text)
	}
}

func TestEdit_NoMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","old_text":"nonexistent","new_text":"replacement"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	textBlock, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(textBlock.Text, "not found") {
		t.Errorf("error text should mention 'not found', got: %q", textBlock.Text)
	}
}

func TestEdit_MultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "repeat\nmiddle\nrepeat\nend\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","old_text":"repeat","new_text":"unique"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	textBlock, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(textBlock.Text, "matched") && !strings.Contains(textBlock.Text, "times") {
		t.Errorf("error text should mention match count, got: %q", textBlock.Text)
	}
}

func TestEdit_SuccessfulEdit_ReturnsSummary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello world\nfoo bar\nbaz qux\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","old_text":"foo bar","new_text":"FOO BAR"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result.Blocks))
	}

	// Block 0: concise summary
	b0, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block[0] is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.HasPrefix(b0.Text, "Edited file: test.txt") {
		t.Errorf("block[0] should start with 'Edited file: test.txt', got %q", b0.Text)
	}
	if !strings.Contains(b0.Text, "1 line changed") {
		t.Errorf("block[0] should contain line change count, got %q", b0.Text)
	}

	// Verify file was changed on disk
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "hello world\nFOO BAR\nbaz qux\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestEdit_PathOutsideWorkspace(t *testing.T) {
	tool := NewEditTool("/tmp/workspace")
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"/etc/passwd","old_text":"root","new_text":"hacker"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true (path outside workspace)")
	}
	if len(result.Blocks) > 0 {
		result, ok := result.Blocks[0].(litellm.TextBlock)
		if ok && result.Text == "" {
			t.Error("expected error text")
		}
	}
}

// ── countLineDiffs tests ───────────────────────────────────────────────────

func TestCountLineDiffs_Identical(t *testing.T) {
	oldLines := []string{"a", "b", "c"}
	newLines := []string{"a", "b", "c"}
	if got := countLineDiffs(oldLines, newLines); got != 0 {
		t.Errorf("countLineDiffs = %d, want 0", got)
	}
}

func TestCountLineDiffs_AllDifferent(t *testing.T) {
	oldLines := []string{"a", "b", "c"}
	newLines := []string{"d", "e", "f"}
	if got := countLineDiffs(oldLines, newLines); got != 3 {
		t.Errorf("countLineDiffs = %d, want 3", got)
	}
}

func TestCountLineDiffs_NewLinesLonger(t *testing.T) {
	oldLines := []string{"a", "b"}
	newLines := []string{"a", "b", "c"}
	if got := countLineDiffs(oldLines, newLines); got != 1 {
		t.Errorf("countLineDiffs = %d, want 1", got)
	}
}

func TestCountLineDiffs_OldLinesLonger(t *testing.T) {
	oldLines := []string{"a", "b", "c"}
	newLines := []string{"a", "b"}
	if got := countLineDiffs(oldLines, newLines); got != 1 {
		t.Errorf("countLineDiffs = %d, want 1", got)
	}
}

func TestCountLineDiffs_EmptySlices(t *testing.T) {
	if got := countLineDiffs(nil, nil); got != 0 {
		t.Errorf("countLineDiffs = %d, want 0", got)
	}
	if got := countLineDiffs([]string{}, []string{}); got != 0 {
		t.Errorf("countLineDiffs = %d, want 0", got)
	}
}
