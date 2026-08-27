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

	manifest := walkWorkspace(dir)
	got := candidatesForPartial(manifest, "")
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

	manifest := walkWorkspace(dir)
	got := candidatesForPartial(manifest, "ap")
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

func TestMentionCandidates_FolderNotExpandedUntilSlash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustMakeDir(t, filepath.Join(dir, "src"))
	mustWriteFile(t, filepath.Join(dir, "src", "util.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "src", "deep.go"), "x\n")

	manifest := walkWorkspace(dir)
	// matching the folder itself keeps it as a single candidate
	got := candidatesForPartial(manifest, "src")
	join := strings.Join(got, ",")
	if !strings.Contains(join, "src/") {
		t.Errorf("candidates %q should keep the src/ folder as a candidate", got)
	}
	if strings.Contains(join, "util.go") || strings.Contains(join, "deep.go") {
		t.Errorf("candidates %q must not expand a folder into its contents until a slash is typed", got)
	}
	// typing the trailing slash descends into the folder
	deep := candidatesForPartial(manifest, "src/")
	deepJoin := strings.Join(deep, ",")
	for _, want := range []string{"src/util.go", "src/deep.go"} {
		if !strings.Contains(deepJoin, want) {
			t.Errorf("deep candidates %q missing %q", deep, want)
		}
	}
}

func TestMentionCandidates_DeepSiblingAndDescendant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustMakeDir(t, filepath.Join(dir, "pkg"))
	mustWriteFile(t, filepath.Join(dir, "pkg", "alpha.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "pkg", "beta.go"), "x\n")
	mustMakeDir(t, filepath.Join(dir, "pkg", "sub"))
	mustWriteFile(t, filepath.Join(dir, "pkg", "sub", "deep.go"), "x\n")

	manifest := walkWorkspace(dir)
	// a partial naming an existing directory surfaces the paths under it
	desc := candidatesForPartial(manifest, "pkg/")
	descJoin := strings.Join(desc, ",")
	for _, want := range []string{"pkg/alpha.go", "pkg/beta.go", "pkg/sub/"} {
		if !strings.Contains(descJoin, want) {
			t.Errorf("descendant candidates %q missing %q", desc, want)
		}
	}
	// a partial matching a sibling basename surfaces those siblings
	sib := candidatesForPartial(manifest, "pkg/al")
	sibJoin := strings.Join(sib, ",")
	for _, want := range []string{"pkg/alpha.go"} {
		if !strings.Contains(sibJoin, want) {
			t.Errorf("sibling candidates %q missing %q", sib, want)
		}
	}
	if strings.Contains(sibJoin, "beta") || strings.Contains(sibJoin, "sub") {
		t.Errorf("sibling candidates %q include non-matching entries", sib)
	}
}

func TestMentionCandidates_HiddenEntriesSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".hidden.go"), "x\n")
	mustMakeDir(t, filepath.Join(dir, ".cache"))
	mustWriteFile(t, filepath.Join(dir, ".cache", "tool.go"), "x\n")
	mustWriteFile(t, filepath.Join(dir, "main.go"), "x\n")

	manifest := walkWorkspace(dir)
	join := strings.Join(manifest, ",")
	if strings.Contains(join, ".hidden") || strings.Contains(join, ".cache") {
		t.Errorf("hidden entries leaked into manifest %q", manifest)
	}
	got := candidatesForPartial(manifest, "")
	gotJoin := strings.Join(got, ",")
	if !strings.Contains(gotJoin, "main.go") {
		t.Errorf("candidates %q missing main.go", got)
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

func TestMention_TrackKicksOffAsyncWalkAndSwaps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "x\n")
	mustMakeDir(t, filepath.Join(dir, "src"))
	mustWriteFile(t, filepath.Join(dir, "src", "util.go"), "x\n")

	mn := NewMention(dir)
	cmd := mn.Track("@", 1)
	if cmd == nil {
		t.Fatal("first mention should request the async workspace walk")
	}
	// walk completes and delivers the manifest in place of the old list
	mn.setManifest(walkWorkspace(dir))
	got := mn.Candidates()
	join := strings.Join(got, ",")
	for _, want := range []string{"main.go", "src/"} {
		if !strings.Contains(join, want) {
			t.Errorf("candidates %q missing %q", got, want)
		}
	}
	// editing the mention re-filters from the cached manifest without a new walk
	if cmd := mn.Track("@s", 2); cmd != nil {
		t.Errorf("re-filter on cached manifest must not start another walk, got cmd")
	}
	if got := mn.SelectedCandidate(); got != "src/" {
		t.Errorf("re-filtered selection = %q, want src/", got)
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
