package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/litellm"
)

func TestGlob_Schema(t *testing.T) {
	tool := NewGlobTool("/tmp")
	if tool.Name() != "glob" {
		t.Errorf("Name = %q, want 'glob'", tool.Name())
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

func TestGlob_InvalidArgs(t *testing.T) {
	tool := NewGlobTool("/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestGlob_EmptyPattern(t *testing.T) {
	tool := NewGlobTool("/tmp")
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
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

func TestGlob_SuccessfulMatch(t *testing.T) {
	dir := t.TempDir()
	files := []string{"foo.go", "bar.go", "sub/qux.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("package main"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	// *.go matches all .go files including nested: bar.go, foo.go, sub/qux.go
	expected := "bar.go\nfoo.go\nsub/qux.go"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

func TestGlob_NoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	// No matches: blocks may be nil or contain empty text
	if len(result.Blocks) > 0 {
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if block.Text != "" {
			t.Errorf("expected empty output, got %q", block.Text)
		}
	}
}

func TestGlob_HiddenDirExclusion(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, "secret.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	// visible.go matches *.go, secret.go is in .hidden (excluded), so visible.go should appear
	visiblePath := filepath.Join(".hidden", "secret.go")
	_ = visiblePath
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	// visible.go should match, .hidden/secret.go should be excluded
	if block.Text != "visible.go" {
		t.Errorf("got %q, want %q", block.Text, "visible.go")
	}
}

func TestGlob_VendorExclusion(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "dep.go"), []byte("package dep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"*"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	// Only main.go should be visible, vendor/ excluded
	expected := "main.go"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

func TestGlob_WorkspaceValidation(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"/etc/passwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true (outside workspace)")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
}

func TestGlob_RecursivePattern(t *testing.T) {
	dir := t.TempDir()
	nestedDir := filepath.Join(dir, "pkg", "sub")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "deep.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"*"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	// * matches everything: root.go, pkg/sub/deep.go
	expected := "pkg/sub/deep.go\nroot.go"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

func TestGlob_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"pattern":"*.go"}`)
	var parsed globArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Pattern != "*.go" {
		t.Errorf("Pattern = %q, want '*.go'", parsed.Pattern)
	}
}

func TestGlob_PathPrefixedPattern(t *testing.T) {
	dir := t.TempDir()
	files := []string{"main.go", "internal/tool/glob.go", "internal/tool/grep.go", "pkg/util/helper.go"}
	for _, f := range files {
		fp := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte("package main"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"internal/tool/*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	// Should match internal/tool/glob.go, internal/tool/grep.go only
	expected := "internal/tool/glob.go\ninternal/tool/grep.go"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

// ── unique tests ───────────────────────────────────────────────────────────

func TestUnique_EmptySlice(t *testing.T) {
	if got := unique(nil); got != nil {
		t.Errorf("unique(nil) = %v, want nil", got)
	}
	if got := unique([]string{}); len(got) != 0 {
		t.Errorf("unique([]string{}) = %v, want empty", got)
	}
}

func TestUnique_NoDuplicates(t *testing.T) {
	input := []string{"a", "b", "c"}
	got := unique(input)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, v := range input {
		if got[i] != v {
			t.Errorf("got[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestUnique_WithDuplicates(t *testing.T) {
	input := []string{"a", "a", "b", "c", "c", "c"}
	got := unique(input)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("got[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestUnique_AllSame(t *testing.T) {
	input := []string{"x", "x", "x", "x"}
	got := unique(input)
	want := []string{"x"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("got[0] = %q, want %q", got[0], want[0])
	}
}
