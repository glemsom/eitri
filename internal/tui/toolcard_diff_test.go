package tui

import (
	"strings"
	"testing"
	"time"
)

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

func cardBody(th Theme, te toolEntry, expanded bool) string {
	all := renderToolEntry(th, te, expanded, time.Time{}, 80, false)
	parts := strings.SplitN(all, "\n", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func TestToolCard_collapsedEditKeepsDeltaSummary(t *testing.T) {
	t.Parallel()
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

func TestToolCard_expandedEditRendersInlineDiff(t *testing.T) {
	t.Parallel()
	te := toolCardDiffEntry("edit", "internal/auth.go", "package auth\n\nfunc Old() {}\n", "package auth\n\nfunc New() {}\n", 1, 1)

	expanded := renderToolEntry(defaultTheme, te, true, time.Time{}, 80, false)

	strip := ansiStrip(expanded)
	if !strings.Contains(strip, "-func Old() {}") {
		t.Errorf("expanded card must render the removed line, got:\n%s", strip)
	}
	if !strings.Contains(strip, "+func New() {}") {
		t.Errorf("expanded card must render the added line, got:\n%s", strip)
	}
	if strings.Contains(expanded, te.result) {
		t.Errorf("expanded edit card must not dump the raw result, got:\n%s", expanded)
	}
	if l := lineContaining(strip, "func New() {}"); l == "" || !strings.HasPrefix(strings.TrimLeft(l, " "), "│") {
		t.Errorf("diff row must carry the card's left border, got line: %q", l)
	}
}

func TestToolCard_expandedWriteDiffStatuses(t *testing.T) {
	t.Parallel()
	add := toolCardDiffEntry("write", "internal/new.go", "", "package new\n\nfunc Fresh() {}\n", 3, 0)
	added := renderToolEntry(defaultTheme, add, true, time.Time{}, 80, false)
	if !strings.Contains(added, "+package new") {
		t.Errorf("added file must render as all-+ diff, got:\n%s", added)
	}
	if strings.Contains(added, "-") && strings.Contains(added, "-package") {
		t.Errorf("added file must carry no removed lines, got:\n%s", added)
	}

	del := toolCardDiffEntry("edit", "internal/gone.go", "package gone\n\nfunc Old() {}\n", "", 0, 3)
	deleted := renderToolEntry(defaultTheme, del, true, time.Time{}, 80, false)
	if !strings.Contains(deleted, "-package gone") {
		t.Errorf("deleted file must render as all-− diff, got:\n%s", deleted)
	}
}

func TestToolCard_expandedNoDiffFallsBackToSummary(t *testing.T) {
	t.Parallel()
	te := toolCardDiffEntry("edit", "internal/auth.go", "", "", 0, 0)

	expanded := renderToolEntry(defaultTheme, te, true, time.Time{}, 80, false)
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
