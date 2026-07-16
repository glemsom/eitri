package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePath_RelativePath(t *testing.T) {
	workspace := t.TempDir()

	// Create a test file
	testFile := filepath.Join(workspace, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateWorkspacePath("test.txt", workspace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != testFile {
		t.Errorf("got %q, want %q", got, testFile)
	}
}

func TestValidatePath_AbsoluteInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	testFile := filepath.Join(workspace, "sub", "test.txt")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateWorkspacePath(testFile, workspace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != testFile {
		t.Errorf("got %q, want %q", got, testFile)
	}
}

func TestValidatePath_RejectDotDotEscape(t *testing.T) {
	workspace := t.TempDir()

	_, err := ValidateWorkspacePath("../etc/passwd", workspace)
	if err == nil {
		t.Fatal("expected error for ../ escape, got nil")
	}
}

func TestValidatePath_RejectAbsoluteOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()

	_, err := ValidateWorkspacePath("/etc/passwd", workspace)
	if err == nil {
		t.Fatal("expected error for absolute path outside workspace, got nil")
	}
}

func TestValidatePath_RejectEmptyPath(t *testing.T) {
	workspace := t.TempDir()

	_, err := ValidateWorkspacePath("", workspace)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestValidatePath_SubdirectoryPath(t *testing.T) {
	workspace := t.TempDir()

	testFile := filepath.Join(workspace, "sub", "nested", "test.txt")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ValidateWorkspacePath("sub/nested/test.txt", workspace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != testFile {
		t.Errorf("got %q, want %q", got, testFile)
	}
}

func TestValidatePath_SkillDirectoryAllowed(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()

	// Use a path unique to skillDir
	testFile := filepath.Join(skillDir, "skill-ref.md")
	if err := os.WriteFile(testFile, []byte("skill ref"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ValidatePathWithAllowed("skill-ref.md", workspace, []string{skillDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Path must be under one of the allowed roots
	inWorkspace := isPathUnder(got, workspace)
	inSkillDir := isPathUnder(got, skillDir)
	if !inWorkspace && !inSkillDir {
		t.Errorf("path %q not under workspace %q or skillDir %q", got, workspace, skillDir)
	}
}

func isPathUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

func TestValidatePath_RejectSkillDirDotDotEscape(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()

	_, err := ValidatePathWithAllowed("../etc/passwd", workspace, []string{skillDir})
	if err == nil {
		t.Fatal("expected error for ../ escape")
	}
}

func TestValidatePath_RejectNonWorkspaceSkillDirEscape(t *testing.T) {
	workspace := t.TempDir()
	skillDir := t.TempDir()

	// file that is in skillDir's parent, not in skillDir
	testFile := filepath.Join(filepath.Dir(skillDir), "neighbor.txt")
	if err := os.WriteFile(testFile, []byte("neighbor"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ValidatePathWithAllowed("../neighbor.txt", workspace, []string{skillDir})
	if err == nil {
		t.Fatal("expected error for path outside allowed roots")
	}
}
