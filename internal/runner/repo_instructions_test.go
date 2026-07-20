package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRepositoryInstructions_FileExists(t *testing.T) {
	dir := t.TempDir()
	content := "# My Repo Instructions\n\nBe careful with files."
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644)

	result, err := readRepositoryInstructions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "<repository_instructions>\n" + content + "\n</repository_instructions>"
	if result != want {
		t.Fatalf("got %q, want %q", result, want)
	}
}

func TestReadRepositoryInstructions_FileMissing(t *testing.T) {
	dir := t.TempDir()

	result, err := readRepositoryInstructions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("got %q, want empty string", result)
	}
}

func TestReadRepositoryInstructions_Over4KBCaps(t *testing.T) {
	dir := t.TempDir()
	// Create content larger than 4KB
	bigContent := strings.Repeat("a", 5000)
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(bigContent), 0644)

	result, err := readRepositoryInstructions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain truncated marker
	if !strings.Contains(result, "[truncated...]") {
		t.Fatalf("truncated content should contain [truncated...], got %q", result)
	}
	// Should be wrapped in tags
	if !strings.HasPrefix(result, "<repository_instructions>\n") {
		t.Fatalf("result should start with <repository_instructions> tag, got %q", result)
	}
	if !strings.HasSuffix(result, "\n</repository_instructions>") {
		t.Fatalf("result should end with </repository_instructions> tag, got %q", result)
	}
}

func TestReadRepositoryInstructions_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(""), 0644)

	result, err := readRepositoryInstructions(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("got %q, want empty string", result)
	}
}
