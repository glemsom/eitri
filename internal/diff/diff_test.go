package diff

import "testing"

// Equal lines collapse to context (' '); changed regions surface as hunks
// with +/- lines and @@ position headers. Expected values are hand-computed
// canonical diffs, independent of the engine's internals (tdd: vertical slice,
// independent source of truth).
func TestDiffReportsChgChanges(t *testing.T) {
	t.Parallel()
	old := "#!/usr/bin/env bash\necho hello\n"
	new := "#!/usr/bin/env bash\necho goodbye\n"
	got := Diff(old, new)
	if len(got) == 0 {
		t.Fatal("expected at least one hunk, got none")
	}
	// Collapse to the flat line list for direct assertion.
	var flat []Line
	for _, h := range got {
		flat = append(flat, h.Lines...)
	}
	want := []Line{
		{' ', "#!/usr/bin/env bash"},
		{'-', "echo hello"},
		{'+', "echo goodbye"},
	}
	if len(flat) != len(want) {
		t.Fatalf("lines = %d, want %d: %+v", len(flat), len(want), flat)
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("line[%d] = %+v, want %+v", i, flat[i], want[i])
		}
	}
}

// Identical inputs produce no hunks at all.
func TestDiffIdenticalIsEmpty(t *testing.T) {
	t.Parallel()
	src := "a\nb\nc\n"
	if got := Diff(src, src); len(got) != 0 {
		t.Errorf("Diff(identical) = %d hunks, want 0", len(got))
	}
}

// A fully new file reports every line as added, with a fresh @@ header.
func TestDiffAddedFile(t *testing.T) {
	t.Parallel()
	got := Diff("", "line1\nline2\n")
	if len(got) != 1 {
		t.Fatalf("hunks = %d, want 1", len(got))
	}
	h := got[0]
	if h.NewStart != 1 || h.NewLines != 2 || h.OldStart != 0 || h.OldLines != 0 {
		t.Errorf("header = old %d:+%d new %d:+%d, want old 0:+0 new 1:+2",
			h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	}
	if len(h.Lines) != 2 || h.Lines[0].Type != '+' || h.Lines[1].Type != '+' {
		t.Errorf("new-file lines = %+v, want two '+' lines", h.Lines)
	}
}

// A deleted file reports every line removed with a zero-length new side.
func TestDiffDeletedFile(t *testing.T) {
	t.Parallel()
	got := Diff("keep\n", "")
	if len(got) != 1 {
		t.Fatalf("hunks = %d, want 1", len(got))
	}
	h := got[0]
	if h.OldStart != 1 || h.OldLines != 1 || h.NewStart != 0 || h.NewLines != 0 {
		t.Errorf("header = old %d:+%d new %d:+%d, want old 1:+1 new 0:+0",
			h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	}
	if len(h.Lines) != 1 || h.Lines[0].Type != '-' {
		t.Errorf("deleted-file lines = %+v, want one '-' line", h.Lines)
	}
}

// Context lines bracket a change so a reader can see where it happened.
func TestDiffIncludesContext(t *testing.T) {
	t.Parallel()
	old := "one\ntwo\nthree\nfour\nfive\nCHANGED\nseven\neight\nnine\nten\n"
	new := "one\ntwo\nthree\nfour\nfive\nchanged\nseven\neight\nnine\nten\n"
	got := Diff(old, new)
	if len(got) != 1 {
		t.Fatalf("hunks = %d, want 1", len(got))
	}
	h := got[0]
	var types string
	for _, l := range h.Lines {
		types += string(l.Type)
	}
	if types != "   -+   " {
		t.Errorf("context window types = %q, want %q (3 context each side)", types, "   -+   ")
	}
}

// Insertion and deletion in one hunk report correct +/- ordering and counts.
func TestDiffMixedInsertAndDelete(t *testing.T) {
	t.Parallel()
	old := "a\nb\nc\nd\n"
	new := "a\nB\nc\nD\n"
	got := Diff(old, new)
	flat := flatten(got)
	want := []Line{
		{' ', "a"},
		{'-', "b"},
		{'+', "B"},
		{' ', "c"},
		{'-', "d"},
		{'+', "D"},
	}
	if len(flat) != len(want) {
		t.Fatalf("lines = %d (%+v), want %d", len(flat), flat, len(want))
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("line[%d] = %+v, want %+v", i, flat[i], want[i])
		}
	}
}

func flatten(hunks []Hunk) []Line {
	var out []Line
	for _, h := range hunks {
		out = append(out, h.Lines...)
	}
	return out
}

// Changes far apart split into separate non-overlapping hunks, each with
// correct 1-based @@ positions (git convention).
func TestDiffSeparatesDistantHunks(t *testing.T) {
	t.Parallel()
	old := stringsJoinLines("h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8", "h9", "h10", "h11", "h12")
	new := stringsJoinLines("h1", "h2", "X3", "h4", "h5", "h6", "h7", "h8", "h9", "h10", "h11", "X12")
	got := Diff(old, new)
	if len(got) != 2 {
		t.Fatalf("hunks = %d (chan 3 and 12 are 8 context lines apart), want 2", len(got))
	}
	// First hunk spans h1..h6 (leading context h1,h2; trailing h4,h5,h6).
	h1 := got[0]
	if h1.OldStart != 1 || h1.OldLines != 6 || h1.NewStart != 1 || h1.NewLines != 6 {
		t.Errorf("hunk1 = old %d:+%d new %d:+%d, want old 1:+6 new 1:+6",
			h1.OldStart, h1.OldLines, h1.NewStart, h1.NewLines)
	}
	if h1.Lines[len(h1.Lines)-1].Text != "h6" {
		t.Errorf("hunk1 tail = %q, want h6 (hunk1 must stop before hunk2)", h1.Lines[len(h1.Lines)-1].Text)
	}
	// Second hunk spans h9..h12 (leading context h9,h10,h11; the h12 change).
	h2 := got[1]
	if h2.OldStart != 9 || h2.OldLines != 4 || h2.NewStart != 9 || h2.NewLines != 4 {
		t.Errorf("hunk2 = old %d:+%d new %d:+%d, want old 9:+4 new 9:+4",
			h2.OldStart, h2.OldLines, h2.NewStart, h2.NewLines)
	}
	// Hunks must not overlap: hunk2's first line is h9, never a duplicate h6.
	if h2.Lines[0].Text != "h9" {
		t.Errorf("hunk2 head = %q, want h9 (no overlap with hunk1)", h2.Lines[0].Text)
	}
}

// Insertion shifts new-side line numbers while the old side stays intact.
func TestDiffHeaderShiftOnInsert(t *testing.T) {
	t.Parallel()
	old := "a\nb\nc\ne\nf\ng\nh\n"
	// Insert a line between c and e (old line 4).
	new := "a\nb\nc\nINS\ne\nf\ng\nh\n"
	got := Diff(old, new)
	if len(got) == 0 {
		t.Fatal("expected a hunk")
	}
	h := got[0]
	// Hunk spans a..g: context a,b,c + insertion + context e,f,g.
	if h.OldStart != 1 || h.OldLines != 6 {
		t.Errorf("old side = %d:+%d, want 1:+6", h.OldStart, h.OldLines)
	}
	if h.NewStart != 1 || h.NewLines != 7 {
		t.Errorf("new side = %d:+%d, want 1:+7 (insertion adds one line)", h.NewStart, h.NewLines)
	}
}

func stringsJoinLines(lines ...string) string {
	s := ""
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}
