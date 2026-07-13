package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello\nworld\nthird line\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != content {
		t.Errorf("content = %q, want %q", result.Content, content)
	}
	if result.Truncated {
		t.Errorf("Truncated = true, want false")
	}
}

func TestReadFile_WithOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line3\nline4\nline5\n"
	if result.Content != want {
		t.Errorf("content = %q, want %q", result.Content, want)
	}
}

func TestReadFile_WithOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 2, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line2\nline3"
	if result.Content != want {
		t.Errorf("content = %q, want %q", result.Content, want)
	}
	if !result.Truncated {
		t.Errorf("Truncated = false, want true")
	}
}

func TestReadFile_TruncatedFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 1, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated {
		t.Errorf("Truncated = false, want true (limit < total lines)")
	}
}

func TestReadFile_NotTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 0, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Truncated {
		t.Errorf("Truncated = true, want false (limit > total lines)")
	}
}

func TestReadFile_NonExistent(t *testing.T) {
	_, err := ReadFile("/nonexistent/path.txt", 0, 0)
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestReadFile_IsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFile(dir, 0, 0)
	if err == nil {
		t.Fatal("expected error for directory, got nil")
	}
}

func TestReadFile_BinaryContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile(path, 0, 0)
	if err == nil {
		t.Fatal("expected error for binary content, got nil")
	}
}

func TestReadFile_OffsetPastEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 100, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Offset past end returns empty (last line after trailing newline is empty)
	if result.Content != "" {
		t.Errorf("content = %q, want %q", result.Content, "")
	}
}

func TestWriteFile_Create(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "newfile.txt")
	content := "hello world"

	result, err := WriteFile(path, content, "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "create" {
		t.Errorf("Mode = %q, want 'create'", result.Mode)
	}
	if result.BytesWritten != len(content) {
		t.Errorf("BytesWritten = %d, want %d", result.BytesWritten, len(content))
	}
	if result.NewContent != content {
		t.Errorf("NewContent = %q, want %q", result.NewContent, content)
	}
	if result.OldContent != "" {
		t.Errorf("OldContent = %q, want empty", result.OldContent)
	}

	// Verify file was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestWriteFile_CreateWithParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "newfile.txt")
	content := "nested content"

	result, err := WriteFile(path, content, "create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "create" {
		t.Errorf("Mode = %q, want 'create'", result.Mode)
	}

	// Verify file was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestWriteFile_CreateExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := WriteFile(path, "new", "create")
	if err == nil {
		t.Fatal("expected error for create on existing file, got nil")
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "editing.txt")
	oldContent := "old content"
	newContent := "new content"
	if err := os.WriteFile(path, []byte(oldContent), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteFile(path, newContent, "overwrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "overwrite" {
		t.Errorf("Mode = %q, want 'overwrite'", result.Mode)
	}
	if result.OldContent != oldContent {
		t.Errorf("OldContent = %q, want %q", result.OldContent, oldContent)
	}
	if result.NewContent != newContent {
		t.Errorf("NewContent = %q, want %q", result.NewContent, newContent)
	}
	if result.BytesWritten != len(newContent) {
		t.Errorf("BytesWritten = %d, want %d", result.BytesWritten, len(newContent))
	}

	// Verify file was updated
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != newContent {
		t.Errorf("file content = %q, want %q", string(data), newContent)
	}
}

func TestWriteFile_OverwriteNonExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.txt")

	_, err := WriteFile(path, "content", "overwrite")
	if err == nil {
		t.Fatal("expected error for overwrite on non-existing file, got nil")
	}
}

func TestWriteFile_OverwriteDirectory(t *testing.T) {
	dir := t.TempDir()

	_, err := WriteFile(dir, "content", "overwrite")
	if err == nil {
		t.Fatal("expected error for overwrite on directory, got nil")
	}
}

func TestWriteFile_InvalidMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	_, err := WriteFile(path, "content", "invalid")
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}

func TestWriteFile_NULContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	_, err := WriteFile(path, "hello\x00world", "create")
	if err == nil {
		t.Fatal("expected error for NUL content, got nil")
	}
}
