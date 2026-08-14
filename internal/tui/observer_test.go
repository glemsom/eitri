package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// newObserverFixture writes a scratch file set and builds a DeltaObserver whose
// injected path-resolution seam maps sandbox paths to the real fixture files
// (issue #174): the observer owns the file reads, the fake resolver only
// translates paths, so unit tests drive the observer with no engine and no
// registry.
func newObserverFixture(t *testing.T) (*DeltaObserver, string) {
	t.Helper()
	dir := t.TempDir()
	resolve := func(sandbox string) string { return filepath.Join(dir, sandbox) }
	return NewDeltaObserver(resolve), dir
}

// TestDeltaObserver_computesEditLineDelta asserts a paired edit tool start +
// result produces the same [+N,-M] line delta and before/after content the
// engine's ToolDelta seam used to report (issue #174, removed in issue #175):
// the observer snapshots the target file on start and diffs it on result. The
// fixture gains two lines as one is swapped for three, so the observer reports
// +2, -0.
func TestDeltaObserver_computesEditLineDelta(t *testing.T) {
	obs, dir := newObserverFixture(t)
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	obs.Start("call_e", "edit", `{"path":"main.go","old_string":"b","new_string":"b\nc\nd"}`)
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatalf("apply edit fixture: %v", err)
	}
	added, removed, before, after, gotPath := obs.Result("call_e", "edit")

	if added != 2 || removed != 0 {
		t.Errorf("delta = +%d-%d, want +2-0", added, removed)
	}
	if before != "a\nb\n" || after != "a\nb\nc\nd\n" {
		t.Errorf("content = before %q after %q, want a\nb\n -> a\nb\nc\nd\n", before, after)
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}
}

// TestDeltaObserver_writeCreatesFile asserts a write tool creating a brand-new
// file reports the full new content as added with empty before (the review
// panel's "added" status source, issue #174 / #90). The count follows the
// engine seam's convention (trailing newline counts as one more line), so
// "x\ny\nz\n" is 4 lines.
func TestDeltaObserver_writeCreatesFile(t *testing.T) {
	obs, dir := newObserverFixture(t)
	path := filepath.Join(dir, "new.go")

	obs.Start("call_w", "write", `{"path":"new.go"}`)
	if err := os.WriteFile(path, []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	added, removed, before, after, gotPath := obs.Result("call_w", "write")

	if added != 4 || removed != 0 {
		t.Errorf("delta = +%d-%d, want +4-0", added, removed)
	}
	if before != "" || after != "x\ny\nz\n" {
		t.Errorf("content = before %q after %q, want empty -> x\ny\nz\n", before, after)
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}
}

// TestDeltaObserver_ignoresNonFileTools asserts non-file tools (bash, read)
// never produce a delta or content — the observer only tracks edit/write.
func TestDeltaObserver_ignoresNonFileTools(t *testing.T) {
	obs, _ := newObserverFixture(t)
	obs.Start("call_b", "bash", `{"command":"ls"}`)
	added, removed, before, after, path := obs.Result("call_b", "bash")
	if added != 0 || removed != 0 || before != "" || after != "" || path != "" {
		t.Errorf("bash observed delta/content = +%d-%d %q->%q path %q, want zeros", added, removed, before, after, path)
	}
}

// TestDeltaObserver_unresolvablePath asserts a path-resolution seam returning
// the empty host path yields a zero delta and no content (best-effort degrade,
// same as the removed engine seam).
func TestDeltaObserver_unresolvablePath(t *testing.T) {
	obs := NewDeltaObserver(func(string) string { return "" })
	obs.Start("call_e", "edit", `{"path":"nowhere"}`)
	added, removed, before, after, path := obs.Result("call_e", "edit")
	if added != 0 || removed != 0 || before != "" || after != "" || path != "" {
		t.Errorf("unresolvable path delta/content = +%d-%d %q->%q path %q, want zeros", added, removed, before, after, path)
	}
}

// TestDeltaObserver_pairsToolCallsById asserts in-flight snapshots are keyed by
// the provider-assigned tool_call id, so each result diffs its own start even
// when multiple file-mutating calls are outstanding (issue #174).
func TestDeltaObserver_pairsToolCallsById(t *testing.T) {
	obs, dir := newObserverFixture(t)
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	if err := os.WriteFile(one, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write one: %v", err)
	}
	if err := os.WriteFile(two, []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write two: %v", err)
	}

	obs.Start("call_1", "edit", `{"path":"one.txt"}`)
	obs.Start("call_2", "edit", `{"path":"two.txt"}`)
	if err := os.WriteFile(one, []byte("one\nextra\n"), 0o644); err != nil {
		t.Fatalf("edit one: %v", err)
	}
	if err := os.WriteFile(two, []byte("two\nmore\nmore\n"), 0o644); err != nil {
		t.Fatalf("edit two: %v", err)
	}

	added1, removed1, before1, _, path1 := obs.Result("call_1", "edit")
	if added1 != 1 || removed1 != 0 || before1 != "one\n" || path1 != one {
		t.Errorf("call_1 delta = +%d-%d before %q path %q, want +1-0 one\n", added1, removed1, before1, path1)
	}
	added2, removed2, before2, after2, path2 := obs.Result("call_2", "edit")
	if added2 != 2 || removed2 != 0 || before2 != "two\n" || after2 != "two\nmore\nmore\n" || path2 != two {
		t.Errorf("call_2 delta = +%d-%d %q->%q path %q, want +2-0 two\n -> two\nmore\nmore\n", added2, removed2, before2, after2, path2)
	}
}

// TestDeltaObserver_missingStartYieldsZero asserts a result with no matching
// start (a non-file tool, or an update whose start was dropped) degrades to a
// zero delta and empty content rather than erroring (issue #174).
func TestDeltaObserver_missingStartYieldsZero(t *testing.T) {
	obs, _ := newObserverFixture(t)
	added, removed, before, after, path := obs.Result("call_x", "edit")
	if added != 0 || removed != 0 || before != "" || after != "" || path != "" {
		t.Errorf("missing-start delta/content = +%d-%d %q->%q path %q, want zeros", added, removed, before, after, path)
	}
}

// TestDeltaObserver_nilResolverIsFailClosed asserts a nil path-resolution seam
// degrades to unresolvable (zero delta, no content) instead of reading sandbox
// paths as host paths — a forgotten wiring must never misreport edits (issue
// #174).
func TestDeltaObserver_nilResolverIsFailClosed(t *testing.T) {
	obs := NewDeltaObserver(nil)
	obs.Start("call_e", "edit", `{"path":"main.go"}`)
	added, removed, before, after, path := obs.Result("call_e", "edit")
	if added != 0 || removed != 0 || before != "" || after != "" || path != "" {
		t.Errorf("nil-resolver delta/content = +%d-%d %q->%q path %q, want zeros", added, removed, before, after, path)
	}
}
