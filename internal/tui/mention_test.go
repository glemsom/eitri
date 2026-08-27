package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtMentionAt_TriggerAtWordBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value   string
		cursor  int // byte offset
		start   int // byte offset of '@'
		partial string
		ok      bool
	}{
		{"@", 1, 0, "", true},                  // bare @ at line start
		{"@src", 4, 0, "src", true},            // typing after @
		{"@src/file", 9, 0, "src/file", true},  // nested path
		{"deploy to @", 11, 10, "", true},      // after space
		{"use (e.g. @", 11, 10, "", true},      // after open paren
		{"see @foo now", 8, 4, "foo", true},    // mid-token after @
		{"foo@bar.com", 8, -1, "", false},      // email: mid-word @ stays literal
		{"no mention", 3, -1, "", false},       // no @ at all
		{"a@b c@d", 6, -1, "", false},          // both @ mid-word stay literal
		{"use @alpha and @", 16, 15, "", true}, // caret on second mention after space
	}
	for _, c := range cases {
		start, partial, ok := atMentionAt(c.value, c.cursor)
		if ok != c.ok {
			t.Errorf("%q@%d: ok = %v, want %v", c.value, c.cursor, ok, c.ok)
			continue
		}
		if c.ok && start != c.start {
			t.Errorf("%q@%d: start = %d, want %d", c.value, c.cursor, start, c.start)
		}
		if c.ok && partial != c.partial {
			t.Errorf("%q@%d: partial = %q, want %q", c.value, c.cursor, partial, c.partial)
		}
	}
}

func TestMentionCandidates_ListsRootEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "app.txt"), "x\n")
	mustMakeDir(t, filepath.Join(dir, "src"))
	mustMakeDir(t, filepath.Join(dir, "vendor"))

	got := mentionCandidates(dir, "")
	join := strings.Join(got, ",")
	for _, want := range []string{"main.go", "app.txt", "src/", "vendor/"} {
		if !strings.Contains(join, want) {
			t.Errorf("candidates %q missing %q", got, want)
		}
	}
	// folders carry a trailing slash and match on the bare prefix
	for _, c := range got {
		if strings.HasSuffix(c, "/") {
			if !strings.HasPrefix(c, "src") && !strings.HasPrefix(c, "vendor") {
				t.Errorf("unexpected folder candidate %q", c)
			}
		}
	}
}

func TestMentionCandidates_FiltersByPartial(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "apple.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "apricot.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "banana.go"), "x\n")

	got := mentionCandidates(dir, "ap")
	join := strings.Join(got, ",")
	if strings.Contains(join, "banana") {
		t.Errorf("candidates %q include non-matching banana.go", got)
	}
	for _, want := range []string{"apple.go", "apricot.go"} {
		if !strings.Contains(join, want) {
			t.Errorf("candidates %q missing %q", got, want)
		}
	}
}

func TestMentionCandidates_FolderNotExpanded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustMakeDir(t, filepath.Join(dir, "src"))
	mustWriteFile(t, filepath.Join(dir, "src", "util.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "src", "deep.go"), "x\n")

	got := mentionCandidates(dir, "src")
	join := strings.Join(got, ",")
	if !strings.Contains(join, "src/") {
		t.Errorf("candidates %q should keep the src/ folder as a candidate", got)
	}
	if strings.Contains(join, "util.go") || strings.Contains(join, "deep.go") {
		t.Errorf("candidates %q must not expand a folder into its contents (deep completion is out of scope)", got)
	}
}

func TestMention_WindowCapsAndScrolls(t *testing.T) {
	t.Parallel()
	mn := NewMention("/tmp/ws")
	mn.open = true
	mn.cands = make([]string, 12)
	for i := range mn.cands {
		mn.cands[i] = fmt.Sprintf("f%02d.go", i)
	}
	mn.idx = 0
	mn.recomputeView()
	if n := mn.CandidateCount(); n > mentionCapRows {
		t.Errorf("visible window = %d rows, want at most %d", n, mentionCapRows)
	}
	if got := mn.SelectedCandidate(); got != mn.cands[0] {
		t.Errorf("leading selection = %q, want %q", got, mn.cands[0])
	}
	// move far enough that the window scrolls to keep the selection visible
	for range 10 {
		mn.Move(1)
	}
	if got := mn.SelectedCandidate(); got != mn.cands[10] {
		t.Errorf("selection after moving = %q, want %q", got, mn.cands[10])
	}
	if n := mn.CandidateCount(); n > mentionCapRows {
		t.Errorf("scrolled window = %d rows, want at most %d", n, mentionCapRows)
	}
	if !containsStr(mn.view, mn.SelectedCandidate()) {
		t.Errorf("selection %q not visible in window %q", mn.SelectedCandidate(), mn.view)
	}
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMakeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
