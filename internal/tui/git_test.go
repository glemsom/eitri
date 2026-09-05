package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitBranch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := GitBranch(filepath.Join(root, "sub", "deep")); got != "main" {
		t.Errorf("GitBranch(subdir) = %q, want main", got)
	}
	if got := GitBranch(root); got != "main" {
		t.Errorf("GitBranch(root) = %q, want main", got)
	}
	if got := GitBranch(""); got != "" {
		t.Errorf("GitBranch(\"\") = %q, want empty", got)
	}

	det := t.TempDir()
	if err := os.MkdirAll(filepath.Join(det, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(det, ".git", "HEAD"), []byte("1a2b3c4d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GitBranch(det); got != "" {
		t.Errorf("GitBranch(detached) = %q, want empty", got)
	}
	if got := GitBranch(t.TempDir()); got != "" {
		t.Errorf("GitBranch(no repo) = %q, want empty", got)
	}
}

func TestRailBranchRenders(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", true, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", true, 250), defaultTheme, defaultRailWidth)
	if contains := strings.Contains(view, "branch"); contains {
		t.Errorf("no branch set: CONTEXT must omit the branch line, got: %q", view)
	}
	r.SetBranch("main")
	view = r.render(NewTelemetry("deepseek-v4-flash", "high", true, 250), defaultTheme, defaultRailWidth)
	if !strings.Contains(view, "branch main") {
		t.Errorf("CONTEXT missing branch line, got: %q", view)
	}
}
