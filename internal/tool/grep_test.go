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
	if !strings.Contains(block.Text, "pattern is required") {
		t.Errorf("expected 'pattern is required' error, got %q", block.Text)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	tool := NewGrepTool("/tmp")
	_, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"[invalid"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
}

func TestGrep_SuccessfulMatch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"hello.go":    "package main\nfunc main() { println(\"hello\") }\n",
		"world.go":    "package main\nfunc main() { println(\"world\") }\n",
		"other.txt":   "this is a text file with hello in it\n",
		"sub/deep.go": "package sub\nfunc Hello() {}\n",
	}
	for name, content := range files {
		fp := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"hello"}`))
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

	// Should find "hello" in: hello.go (line 2), other.txt (line 1), sub/deep.go (line 2, "Hello" case-sensitive)
	// "hello" in hello.go:2, other.txt:1 (both lowercase hello)
	// "Hello" in sub/deep.go:2 won't match because case-sensitive
	expectedLines := []string{"hello.go:2:func main() { println(\"hello\") }", "other.txt:1:this is a text file with hello in it"}
	for _, el := range expectedLines {
		if !strings.Contains(block.Text, el) {
			t.Errorf("expected output to contain %q, got:\n%s", el, block.Text)
		}
	}
	// sub/deep.go:2 has "Hello" with capital H, case-sensitive search for "hello" should NOT match
	if strings.Contains(block.Text, "sub/deep.go") {
		t.Errorf("output should not contain sub/deep.go for case-sensitive 'hello' search")
	}
}

func TestGrep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"zzznonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	// No matches: blocks may be nil or contain empty text
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
		"greeting.go":  "package main\nfunc hi() { println(\"hello\") }\n",
		"greeting.py":  "def hi():\n    print(\"hello\")\n",
		"data.txt":     "hello world\n",
		"cmd/main.go":  "package main\nfunc main() {}\n",
	}
	for name, content := range files {
		fp := filepath.Join(dir, name)
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

	// Should only match greeting.go (line 1, "hello" in func hi)
	if !strings.Contains(block.Text, "greeting.go") {
		t.Errorf("expected greeting.go in output, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "greeting.py") {
		t.Errorf("output should not contain greeting.py (file_pattern=*.go)")
	}
	if strings.Contains(block.Text, "data.txt") {
		t.Errorf("output should not contain data.txt (file_pattern=*.go)")
	}
}

func TestGrep_OutputTruncation(t *testing.T) {
	dir := t.TempDir()

	// Create a file with many matching lines to exceed 128 KiB
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, "match line content here")
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0o644); err != nil {
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

	// Output should end with the truncation marker
	if !strings.HasSuffix(block.Text, "... (output truncated at 128 KiB)") {
		t.Errorf("expected output to be truncated, got length %d, suffix %q", len(block.Text), block.Text[len(block.Text)-40:])
	}
}

func TestGrep_HiddenDirExclusion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden", "secret.go"), []byte("package main\nfunc secret() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package main\nfunc visible() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"func"}`))
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

	// visible.go should match, .hidden/secret.go should be excluded
	if !strings.Contains(block.Text, "visible.go") {
		t.Errorf("expected visible.go in output, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "secret.go") {
		t.Errorf("output should not contain secret.go from hidden directory")
	}
}

func TestGrep_VendorExclusion(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "dep.go"), []byte("package dep\nfunc depFunc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"pattern":"func"}`))
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

	// Only main.go should be visible, vendor/ excluded
	if !strings.Contains(block.Text, "main.go") {
		t.Errorf("expected main.go in output, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "dep.go") {
		t.Errorf("output should not contain dep.go from vendor directory")
	}
}

func TestGrep_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"pattern":"func","file_pattern":"*.go"}`)
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Pattern != "func" {
		t.Errorf("Pattern = %q, want 'func'", parsed.Pattern)
	}
	if parsed.FilePattern != "*.go" {
		t.Errorf("FilePattern = %q, want '*.go'", parsed.FilePattern)
	}
}
