package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ────────────────────────────────────────────────────────────────

func lineHash(line string) string {
	h := sha256.Sum256([]byte(line))
	return fmt.Sprintf("%x", h)[:8]
}

// ── ReadFile ───────────────────────────────────────────────────────────────

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

func TestReadFile_InvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.txt")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xfd}, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadFile(path, 0, 0)
	if err == nil {
		t.Fatal("expected error for invalid UTF-8, got nil")
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

func TestReadFile_UnlimitedZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	// Write 150 lines
	lines := make([]string, 150)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// limit=0 now means unlimited
	result, err := ReadFile(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != content {
		t.Errorf("content length = %d, want full %d", len(result.Content), len(content))
	}
	if result.Truncated {
		t.Errorf("Truncated = true, want false (limit=0 means unlimited)")
	}
}

func TestReadFile_NextOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Read first chunk with explicit limit=100
	result, err := ReadFile(path, 0, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalLines != 250 {
		t.Errorf("TotalLines = %d, want %d", result.TotalLines, 250)
	}
	if result.NextOffset != 101 {
		t.Errorf("NextOffset = %d, want %d", result.NextOffset, 101)
	}

	// Read second chunk
	result2, err := ReadFile(path, 101, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.NextOffset != 201 {
		t.Errorf("NextOffset = %d, want %d", result2.NextOffset, 201)
	}

	// Read third chunk (final 50 lines)
	result3, err := ReadFile(path, 201, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result3.NextOffset != 0 {
		t.Errorf("NextOffset = %d, want 0 (all returned)", result3.NextOffset)
	}
	if result3.Truncated {
		t.Errorf("Truncated = true, want false (final chunk)")
	}
}

func TestReadFile_TotalLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "a\nb\nc\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalLines != 4 {
		t.Errorf("TotalLines = %d, want %d", result.TotalLines, 4)
	}
}

// ── ReadFile with line info ────────────────────────────────────────────────

func TestReadFile_LineInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello\nworld\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFile(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Without includeLineInfo, content is plain
	wantPlain := "hello\nworld\n"
	if result.Content != wantPlain {
		t.Errorf("Content = %q, want %q", result.Content, wantPlain)
	}

	// With includeLineInfo, content has LINE:HASH prefixes
	result2, err := ReadFileWithLineInfo(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(result2.Content, "\n")
	if len(lines) != 3 { // 2 content lines + trailing empty
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	wantLine1 := fmt.Sprintf("1:%s | hello", lineHash("hello"))
	wantLine2 := fmt.Sprintf("2:%s | world", lineHash("world"))
	if lines[0] != wantLine1 {
		t.Errorf("line 1 = %q, want %q", lines[0], wantLine1)
	}
	if lines[1] != wantLine2 {
		t.Errorf("line 2 = %q, want %q", lines[1], wantLine2)
	}
}

func TestReadFile_LineInfoEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFileWithLineInfo(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("Content = %q, want empty", result.Content)
	}
}

func TestReadFile_LineInfoSingleLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.txt")
	content := "just one line"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFileWithLineInfo(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("1:%s | just one line", lineHash("just one line"))
	if result.Content != want {
		t.Errorf("Content = %q, want %q", result.Content, want)
	}
}

func TestReadFile_LineInfoSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "special.txt")
	content := "func main() {\n\treturn 42\n}"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFileWithLineInfo(path, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	want1 := fmt.Sprintf("1:%s | func main() {", lineHash("func main() {"))
	want2 := fmt.Sprintf("2:%s | \treturn 42", lineHash("\treturn 42"))
	want3 := fmt.Sprintf("3:%s | }", lineHash("}"))
	if lines[0] != want1 {
		t.Errorf("line 1 = %q, want %q", lines[0], want1)
	}
	if lines[1] != want2 {
		t.Errorf("line 2 = %q, want %q", lines[1], want2)
	}
	if lines[2] != want3 {
		t.Errorf("line 3 = %q, want %q", lines[2], want3)
	}
}

func TestReadFile_LineInfoWithOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "a\nb\nc\nd\ne\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := ReadFileWithLineInfo(path, 2, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(result.Content, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	want1 := fmt.Sprintf("2:%s | b", lineHash("b"))
	want2 := fmt.Sprintf("3:%s | c", lineHash("c"))
	if lines[0] != want1 {
		t.Errorf("line 1 = %q, want %q", lines[0], want1)
	}
	if lines[1] != want2 {
		t.Errorf("line 2 = %q, want %q", lines[1], want2)
	}
}

// ── LineHash ───────────────────────────────────────────────────────────────

func TestLineHash_Consistent(t *testing.T) {
	h1 := LineHash("hello world")
	h2 := LineHash("hello world")
	if h1 != h2 {
		t.Errorf("line hash not consistent: %q vs %q", h1, h2)
	}
}

func TestLineHash_DifferentStrings(t *testing.T) {
	h1 := LineHash("foo")
	h2 := LineHash("bar")
	if h1 == h2 {
		t.Errorf("different strings should have different hashes")
	}
}

func TestLineHash_Length(t *testing.T) {
	h := LineHash("test")
	if len(h) != 8 {
		t.Errorf("hash length = %d, want 8", len(h))
	}
}

// ── WriteFile (unchanged, keep existing) ───────────────────────────────────

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
	wantDirs := []string{filepath.Join(dir, "sub"), filepath.Join(dir, "sub", "nested")}
	if len(result.DirsCreated) != len(wantDirs) {
		t.Fatalf("DirsCreated len = %d, want %d (%v)", len(result.DirsCreated), len(wantDirs), result.DirsCreated)
	}
	for i := range wantDirs {
		if result.DirsCreated[i] != wantDirs[i] {
			t.Fatalf("DirsCreated[%d] = %q, want %q", i, result.DirsCreated[i], wantDirs[i])
		}
	}

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
