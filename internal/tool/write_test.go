package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestWrite_Schema(t *testing.T) {
	tool := NewWriteTool("/tmp", nil, nil)
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
	tool := NewWriteTool("/tmp", nil, nil)
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestWrite_EmptyPath(t *testing.T) {
	tool := NewWriteTool("/tmp", nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"","content":"hello"}`))
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
	if !strings.Contains(block.Text, "path is required") {
		t.Errorf("expected 'path is required' error, got %q", block.Text)
	}
}

func TestWrite_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"../../etc/passwd","content":"hack"}`))
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

	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"sub/hello.txt","content":"hello world"}`))
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
	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/deep.txt","content":"deep content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
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
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
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

	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"existing.txt","content":"new content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
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
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "overwrite") {
		t.Errorf("output should mention 'overwrite', got %q", block.Text)
	}
}

func TestWrite_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt"}`))
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
	if !strings.Contains(block.Text, "content is required") {
		t.Errorf("expected 'content is required' error, got %q", block.Text)
	}
}

func TestWrite_EmptyContentCreatesZeroByteFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"empty.txt","content":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false: %#v", result.Blocks)
	}
	data, err := os.ReadFile(filepath.Join(dir, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("file length = %d, want 0", len(data))
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "Wrote 0 bytes") {
		t.Errorf("expected zero-byte success message, got %q", block.Text)
	}
}

func TestWrite_EmptyContentTruncatesExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"existing.txt","content":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true, want false: %#v", result.Blocks)
	}
	info, err := os.Stat(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("file size = %d, want 0", info.Size())
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "Wrote 0 bytes") || !strings.Contains(block.Text, "overwrite") {
		t.Errorf("expected zero-byte overwrite success message, got %q", block.Text)
	}
}

func TestWrite_ReportsDirectoriesCreated(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir, nil, nil)

	// Writing to a deeply nested path should report directories created
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/newfile.txt","content":"content"}`))
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

	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"existing/file.txt","content":"hello"}`))
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

	tool := NewWriteTool(dir, nil, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"a/b/c/d/partial.txt","content":"partial"}`))
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

	// Output should mention directories created
	if !strings.Contains(block.Text, "2 dirs created") {
		t.Errorf("expected '2 dirs created' in output, got %q", block.Text)
	}
}

// ── countNewDirs tests ─────────────────────────────────────────────────────

func TestCountNewDirs_ExistingPath(t *testing.T) {
	dir := t.TempDir()
	if got := countNewDirs(dir); got != 0 {
		t.Errorf("countNewDirs(%q) = %d, want 0", dir, got)
	}
}

func TestCountNewDirs_OneNewDir(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "newdir")
	if got := countNewDirs(newPath); got != 1 {
		t.Errorf("countNewDirs(%q) = %d, want 1", newPath, got)
	}
}

func TestCountNewDirs_MultipleNewDirs(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "a", "b", "c")
	if got := countNewDirs(newPath); got != 3 {
		t.Errorf("countNewDirs(%q) = %d, want 3", newPath, got)
	}
}

// ── writable-root targets (issue #1210) ─────────────────────────────────────

func TestWrite_WritableRootTarget(t *testing.T) {
	workspace := t.TempDir()
	writableRoot := t.TempDir()

	tool := NewWriteTool(workspace, []string{writableRoot}, nil)
	target := filepath.Join(writableRoot, "out", "report.txt")
	result, err := tool.Call(context.Background(),
		json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"from writable root"}`, target)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("result.IsError = true, want false: %#v", result.Blocks)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "from writable root" {
		t.Errorf("file content = %q, want %q", string(data), "from writable root")
	}
}

func TestWrite_OutsideAllRootsHardError(t *testing.T) {
	workspace := t.TempDir()
	writableRoot := t.TempDir()

	tool := NewWriteTool(workspace, []string{writableRoot}, nil)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"path":"/etc/passwd","content":"hack"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true (hard error for out-of-policy write)")
	}
	if result.NeedsConfirm {
		t.Error("result.NeedsConfirm = true, want false (out-of-policy write must not prompt)")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "outside allowed directories") {
		t.Errorf("expected hard error about outside allowed directories, got %q", block.Text)
	}
}

func TestWrite_TmpRewrittenWhenSandboxed(t *testing.T) {
	workspace := t.TempDir()
	hostDir := t.TempDir() // the session-scoped sandbox tmpdir on the host (ADR-0026)

	tool := NewWriteTool(workspace, []string{hostDir}, func(sessionID string) (string, bool) {
		if sessionID != "sess-tmp" {
			t.Fatalf("unexpected session ID %q", sessionID)
		}
		return hostDir, true
	})
	ctx := context.WithValue(context.Background(), SessionIDKey, "sess-tmp")

	result, err := tool.Call(ctx, json.RawMessage(`{"path":"/tmp/out/report.txt","content":"shadow content"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("result.IsError = true, want false: %#v", result.Blocks)
	}

	want := filepath.Join(hostDir, "out", "report.txt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "shadow content" {
		t.Errorf("file content = %q, want %q", string(data), "shadow content")
	}
}

func TestWrite_TmpPassthroughWhenUnsandboxed(t *testing.T) {
	workspace := t.TempDir()
	hostTmp, err := os.MkdirTemp("/tmp", "eitri-write-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(hostTmp) })

	// A genuine /tmp/... target. No tmpdir lookup (sandbox none / bwrap
	// unavailable) → the path passes through unchanged to host /tmp.
	rel := strings.TrimPrefix(hostTmp, "/tmp/")
	target := filepath.Join("/tmp", rel, "note.txt")

	tool := NewWriteTool(workspace, []string{"/tmp"}, nil)
	result, err := tool.Call(context.Background(),
		json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"host tmp"}`, target)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("result.IsError = true, want false: %#v", result.Blocks)
	}

	data, err := os.ReadFile(filepath.Join(hostTmp, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "host tmp" {
		t.Errorf("file content = %q, want %q", string(data), "host tmp")
	}
}

func TestWrite_SchemaDescribesWritableRoots(t *testing.T) {
	tool := NewWriteTool("/tmp", nil, nil)
	var schemaMap map[string]any
	if err := json.Unmarshal(tool.JSONSchema(), &schemaMap); err != nil {
		t.Fatal(err)
	}
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	pathProp, ok := props["path"].(map[string]any)
	if !ok {
		t.Fatal("schema missing path property")
	}
	desc, ok := pathProp["description"].(string)
	if !ok {
		t.Fatal("path property missing description")
	}
	if !strings.Contains(desc, "writable") {
		t.Errorf("path description should mention writable roots, got %q", desc)
	}
	if !strings.Contains(desc, "/tmp") {
		t.Errorf("path description should mention the /tmp mapping, got %q", desc)
	}
}
