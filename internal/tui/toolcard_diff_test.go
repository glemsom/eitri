package tui

import (
	"strings"
	"testing"
	"time"
)

// toolCardDiffEntry returns a completed file-mutating tool entry carrying the
// before/after content, path, and line-delta metadata a real edit/write result
// delivers, so the expanded tool card has the same raw material the review
// projection reads.
func toolCardDiffEntry(name, path, before, after string, added, removed int) toolEntry {
	te := toolEntryFor(name, `{"path":"`+path+`"}`)
	te.result = "ok (1ms)  1 file changed\n"
	te.lines = 1
	te.path = path
	te.before = before
	te.after = after
	te.added = added
	te.removed = removed
	te.complete = true
	return te
}

// cardBody returns the entry's rendered rows below the head (the collapsed
// summary or the expanded card block), so assertions never depend on the
// styling of the head row.
func cardBody(th Theme, te toolEntry, expanded bool) string {
	all := renderToolEntry(th, te, expanded, time.Time{}, 80)
	parts := strings.SplitN(all, "\n", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// TestToolCard_collapsedEditKeepsDeltaSummary asserts a collapsed edit/write
// card keeps today's [+N,−M] summary: the before/after file content never
// renders, and the delta tag text stays on the head.
func TestToolCard_collapsedEditKeepsDeltaSummary(t *testing.T) {
	te := toolCardDiffEntry("edit", "internal/auth.go", "package auth\n\nfunc Old() {}\n", "package auth\n\nfunc New() {}\n", 1, 1)

	head := toolEntryHead(te)
	if !strings.Contains(head, "[+1, −1]") {
		t.Errorf("collapsed head must keep the [+N,−M] delta tag, got %q", head)
	}

	body := cardBody(defaultTheme, te, false)
	if strings.Contains(body, "func Old") || strings.Contains(body, "func New") {
		t.Errorf("collapsed card must not leak before/after content, got %q", body)
	}
	if strings.Contains(strings.ReplaceAll(body, "\x1b[m", ""), "│") {
		t.Errorf("collapsed card must not render the expanded diff card, got %q", body)
	}
}

// TestToolCard_expandedEditRendersInlineDiff asserts an expanded edit/write card
// renders the before→after file content as an inline diff — hunks with the
// git-style @@ header, styled +/- lines, and the left card border framing —
// instead of the raw result dump.
func TestToolCard_expandedEditRendersInlineDiff(t *testing.T) {
	te := toolCardDiffEntry("edit", "internal/auth.go", "package auth\n\nfunc Old() {}\n", "package auth\n\nfunc New() {}\n", 1, 1)

	expanded := renderToolEntry(defaultTheme, te, true, time.Time{}, 80)

	// Diff lines render with the diff engine's +/- vocabulary.
	strip := ansiStrip(expanded)
	if !strings.Contains(strip, "-func Old() {}") {
		t.Errorf("expanded card must render the removed line, got:\n%s", strip)
	}
	if !strings.Contains(strip, "+func New() {}") {
		t.Errorf("expanded card must render the added line, got:\n%s", strip)
	}
	// The result dump must not be what the card shows.
	if strings.Contains(expanded, te.result) {
		t.Errorf("expanded edit card must not dump the raw result, got:\n%s", expanded)
	}
	// The card keeps the tool's category-colored left border (its framing):
	// the diff row is prefixed by the file-category-hued border char.
	if l := lineContaining(strip, "func New() {}"); l == "" || !strings.HasPrefix(strings.TrimLeft(l, " "), "│") {
		t.Errorf("diff row must carry the card's left border, got line: %q", l)
	}
}

// TestToolCard_expandedWriteDiffStatuses asserts added and deleted paths render
// as all-+ and all-− diffs, derived from the before/after content itself.
func TestToolCard_expandedWriteDiffStatuses(t *testing.T) {
	// Added: empty before, content after.
	add := toolCardDiffEntry("write", "internal/new.go", "", "package new\n\nfunc Fresh() {}\n", 3, 0)
	added := renderToolEntry(defaultTheme, add, true, time.Time{}, 80)
	if !strings.Contains(added, "+package new") {
		t.Errorf("added file must render as all-+ diff, got:\n%s", added)
	}
	if strings.Contains(added, "-") && strings.Contains(added, "-package") {
		t.Errorf("added file must carry no removed lines, got:\n%s", added)
	}

	// Deleted: content before, empty after.
	del := toolCardDiffEntry("edit", "internal/gone.go", "package gone\n\nfunc Old() {}\n", "", 0, 3)
	deleted := renderToolEntry(defaultTheme, del, true, time.Time{}, 80)
	if !strings.Contains(deleted, "-package gone") {
		t.Errorf("deleted file must render as all-− diff, got:\n%s", deleted)
	}
}

// TestToolCard_expandedNoDiffFallsBackToSummary asserts an expanded card whose
// before/after are both empty falls back to the [+N, −M] count-summary card
// body (existing review behavior), never the raw result dump.
// The dump check keys on the stripped result text, not the full te.result
// string: the card frame's trailing border space would mask a rendered dump.
func TestToolCard_expandedNoDiffFallsBackToSummary(t *testing.T) {
	te := toolCardDiffEntry("edit", "internal/auth.go", "", "", 0, 0)

	expanded := renderToolEntry(defaultTheme, te, true, time.Time{}, 80)
	strip := ansiStrip(expanded)
	if !strings.Contains(strip, "[+0, −0]") {
		t.Errorf("no-diff fallback must keep the count summary, got:\n%s", strip)
	}
	if strings.Contains(strip, "1 file changed") {
		t.Errorf("no-diff fallback must not dump the raw result, got:\n%s", strip)
	}
	if di := strings.Index(strip, "[+0, −0]"); di < 0 || !strings.Contains(strip[:di], "internal/auth.go") {
		t.Errorf("count summary must name the file, got:\n%s", strip)
	}
}
