package fileutil

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveWritablePath_WorkspaceTarget verifies a target inside the
// workspace root resolves to the cleaned absolute path.
func TestResolveWritablePath_WorkspaceTarget(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "sub", "file.txt")

	got, err := ResolveWritablePath("sub/file.txt", workspace, nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

// TestResolveWritablePath_WritableRootTarget verifies an absolute target
// inside one of the configured writable roots resolves (the write/edit seam
// for paths outside the workspace root).
func TestResolveWritablePath_WritableRootTarget(t *testing.T) {
	workspace := t.TempDir()
	writableRoot := t.TempDir()
	target := filepath.Join(writableRoot, "nested", "out.txt")

	got, err := ResolveWritablePath(target, workspace, []string{writableRoot}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

// TestResolveWritablePath_OutsideAllRoots verifies a target outside the
// workspace and every writable root is a hard error (no confirmation prompt).
func TestResolveWritablePath_OutsideAllRoots(t *testing.T) {
	workspace := t.TempDir()

	_, err := ResolveWritablePath("/etc/passwd", workspace, nil, "", nil)
	if err == nil {
		t.Fatal("expected hard error for target outside all roots, got nil")
	}
}

// TestResolveWritablePath_TmpMappedWhenTracked verifies a /tmp/... target is
// rewritten to the session's sandbox tmpdir on the host when that tmpdir is
// tracked for the session (ADR-0026), and then validates against the roots.
func TestResolveWritablePath_TmpMappedWhenTracked(t *testing.T) {
	workspace := t.TempDir()
	hostDir := t.TempDir()

	got, err := ResolveWritablePath("/tmp/report.html", workspace, []string{hostDir}, "sess-1",
		func(sessionID string) (string, bool) {
			if sessionID != "sess-1" {
				t.Fatalf("unexpected session ID %q", sessionID)
			}
			return hostDir, true
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(hostDir, "report.html")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveWritablePath_TmpPassthroughWhenUntracked verifies a /tmp/...
// target passes through unchanged when the session's tmpdir is not tracked —
// identical fallback semantics to open_in_browser (ADR-0026).
func TestResolveWritablePath_TmpPassthroughWhenUntracked(t *testing.T) {
	workspace := t.TempDir()

	got, err := ResolveWritablePath("/tmp/report.html", workspace, []string{"/tmp"}, "sess-1",
		func(sessionID string) (string, bool) {
			return "", false
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/report.html" {
		t.Errorf("got %q, want %q", got, "/tmp/report.html")
	}
}

// TestResolveWritablePath_RejectsDotDotEscape verifies .. escapes are rejected
// for both relative targets and absolute /tmp targets (where the escape
// survives the /tmp rewrite and must still be caught by validation).
func TestResolveWritablePath_RejectsDotDotEscape(t *testing.T) {
	workspace := t.TempDir()
	hostDir := t.TempDir()

	for _, tc := range []struct {
		name   string
		path   string
		lookup TmpdirFor
	}{
		{name: "relative", path: "../etc/passwd"},
		{name: "absolute", path: filepath.Join(workspace, "..", "etc", "passwd")},
		{name: "tmp-rewritten", path: "/tmp/../../etc/passwd", lookup: func(string) (string, bool) { return hostDir, true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveWritablePath(tc.path, workspace, []string{hostDir}, "sess-1", tc.lookup)
			if err == nil {
				t.Fatal("expected error for .. escape, got nil")
			}
		})
	}
}

// TestResolveWritablePath_RejectsEmptyPath verifies an empty path is rejected.
func TestResolveWritablePath_RejectsEmptyPath(t *testing.T) {
	workspace := t.TempDir()

	_, err := ResolveWritablePath("", workspace, nil, "", nil)
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

// TestResolveWritablePath_TmpMappedOutsideRoots verifies that after a /tmp/...
// target is rewritten to the session tmpdir, the rewritten path is still
// validated — a rewritten target outside every root is a hard error.
func TestResolveWritablePath_TmpMappedOutsideRoots(t *testing.T) {
	workspace := t.TempDir()
	hostDir := t.TempDir()

	// The rewritten path lives outside every root → hard error.
	_, err := ResolveWritablePath("/tmp/secret.txt", workspace, nil, "sess-1",
		func(string) (string, bool) { return hostDir, true })
	if err == nil {
		t.Fatal("expected hard error for rewritten target outside all roots, got nil")
	}
	if !strings.Contains(err.Error(), "outside allowed directories") {
		t.Errorf("error %q should report the path as outside allowed directories", err)
	}
}
