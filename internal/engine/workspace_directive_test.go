package engine

import (
	"strings"
	"testing"
)

// TestWorkspaceDirectiveNamesWritablePaths guards the per-run statement of what
// the run may write. Without it the model learns the boundary only by writing
// outside it and reading EROFS, which costs turns on every infrastructure tool
// that keeps state under $HOME.
func TestWorkspaceDirectiveNamesWritablePaths(t *testing.T) {
	t.Parallel()
	writable := []string{"/srv/work", "/home/u/.eitri/sessions/abc/tmp", "/home/u/.kube"}
	got := workspaceDirective("/srv/work", writable)

	if !strings.Contains(got, "## Working directory") || !strings.Contains(got, "/srv/work") {
		t.Fatalf("directive lost the working-directory statement: %q", got)
	}
	if !strings.Contains(got, "## Write permissions") {
		t.Fatalf("directive missing the write-permissions section: %q", got)
	}
	for _, p := range writable {
		if !strings.Contains(got, p) {
			t.Errorf("directive omits writable path %q: %q", p, got)
		}
	}
	if !strings.Contains(got, "read-only") {
		t.Errorf("directive must state that every other path is read-only: %q", got)
	}
}

// TestWorkspaceDirectiveOmitsWriteSectionWhenUnconfined guards the
// unsandboxed (--yolo-unsafe) case: with no writable subset to report the run
// must not claim one, and the bash tool description is what already states the
// absence of a sandbox.
func TestWorkspaceDirectiveOmitsWriteSectionWhenUnconfined(t *testing.T) {
	t.Parallel()
	got := workspaceDirective("/srv/work", nil)

	if strings.Contains(got, "## Write permissions") || strings.Contains(got, "read-only") {
		t.Fatalf("unconfined run must not claim a write boundary: %q", got)
	}
	if !strings.Contains(got, "## Working directory") {
		t.Fatalf("directive lost the working-directory statement: %q", got)
	}
}
