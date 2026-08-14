package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Hardware caret policy (issue #168): the composer's caret is the terminal's
// hardware cursor — not the reverse-video software cell bubbles v2 paints by
// default — and it must track the true edit position across every composer
// state. Its shape and blink follow the explicit caret style policy (issue
// #170): a steady block. Tests drive the public Update/View seam and assert on
// the attached tea.View.Cursor (cell coordinates, 0-indexed from the frame's
// top-left) and the rendered surface.
//
// The composer's internal viewport scrolls as the draft wraps/grows, so the
// caret's expected frame position is derived from the rendered surface — the
// caret must sit at the end (or on) the visible row that renders the edit
// line — rather than from hardcoded scroll state.

// caretModel builds a sized chat model with a small transcript, so the band
// layout is deterministic: separator row + composer rows pinned at the bottom
// of the 80x24 frame, no status strip (no telemetry), no slash completion.
func caretModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	return resize(t, m)
}

// caret returns the frame's attached hardware caret, failing the test when the
// composer is expected to be the active surface but no caret is attached.
func caret(t *testing.T, m Model) tea.Cursor {
	t.Helper()
	c := m.View().Cursor
	if c == nil {
		t.Fatal("hardware caret must be attached while the composer is the active surface")
	}
	return *c
}

// newline inserts a line break in the composer the way legacy terminals
// deliver Shift+Enter — the line-feed byte surfaced as KeyCtrlJ (issue #121
// AC2).
func newline(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	return asModel(t, nm)
}

// TestComposer_HardwareCaretReplacesSoftwareCell asserts the composer paints no
// reverse-video caret cell while focused — the character under the caret is
// plain text — and the frame attaches the terminal's hardware caret instead
// (issue #168 AC1).
func TestComposer_HardwareCaretReplacesSoftwareCell(t *testing.T) {
	m := caretModel(t)
	m = typeText(t, m, "hi")
	if strings.Contains(view(m), "\x1b[7m") {
		t.Errorf("composer must not render a software reverse-video caret cell, got:\n%s", view(m))
	}
	caret(t, m)
}

// TestComposer_CaretStylePolicy asserts the explicit caret style policy
// (issue #170): the composer's hardware caret is a steady (non-blinking) block,
// requested deliberately rather than inherited from the textarea default or the
// terminal's own settings. A terminal that ignores the shape request falls back
// to its own default block caret — still visible, never hidden.
func TestComposer_CaretStylePolicy(t *testing.T) {
	m := caretModel(t)
	c := caret(t, m)
	if c.Shape != tea.CursorBlock {
		t.Errorf("caret shape = %v, want block", c.Shape)
	}
	if c.Blink {
		t.Error("caret must be steady (no blink)")
	}
}

// TestComposer_CaretTracksTyping asserts the hardware caret follows the edit
// position on a single line: at the prompt end when empty, then one column per
// typed rune, on the composer's bottom row (issue #168 AC2). The bottom row is
// the rendered frame's last row — the band is pinned to the bottom.
func TestComposer_CaretTracksTyping(t *testing.T) {
	m := caretModel(t)
	bottom := lineCount(view(m)) - 1
	if c := caret(t, m); c.X != 2 || c.Y != bottom {
		t.Errorf("empty-composer caret = (%d,%d), want (2,%d)", c.X, c.Y, bottom)
	}
	m = typeText(t, m, "hi")
	if c := caret(t, m); c.X != 4 || c.Y != bottom {
		t.Errorf("caret after typing %q = (%d,%d), want (4,%d)", "hi", c.X, c.Y, bottom)
	}
}

// TestComposer_CaretTracksWrappedDraft asserts the caret stays on the true
// edit cell when the draft soft-wraps: a draft longer than one composer row
// wraps, and the caret sits at the end of the last rendered draft line (issue
// #168 AC2, soft-wrapped lines).
func TestComposer_CaretTracksWrappedDraft(t *testing.T) {
	m := caretModel(t)
	m = typeText(t, m, strings.Repeat("a", 100)) // wraps to two composer rows
	if rows := composerRows(m); len(rows) < 2 {
		t.Fatalf("draft must wrap to at least two composer rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "a")
}

// TestComposer_CaretTracksMultiLineDraft asserts the caret follows the edit
// line as the composer grows within the band (issue #168 AC2): each new line
// pushes the band up a row, the caret sits on the new line's visible row, and
// cursor navigation moves it within the grown composer.
func TestComposer_CaretTracksMultiLineDraft(t *testing.T) {
	m := caretModel(t)
	m = typeText(t, m, "line one")
	m = newline(t, m)
	m = typeText(t, m, "line two")
	if rows := composerRows(m); len(rows) != 2 {
		t.Fatalf("two-line draft must grow the composer to 2 rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "line two")
	// CursorUp moves the caret to the first draft line.
	m = asModel(t, mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp}))
	caretAtEndOfVisibleRow(t, m, "line one")
}

// TestComposer_CaretTracksInternalScroll asserts the caret never goes stale
// when the draft exceeds the composer's bound and the band scrolls internally
// (issue #168 AC2): with more draft rows than maxComposerRows, the caret stays
// on the visible row that renders the edit line, at the correct column,
// instead of drifting above the band.
func TestComposer_CaretTracksInternalScroll(t *testing.T) {
	m := caretModel(t)
	// 9 draft rows exceed the 8-row composer bound, forcing internal scroll.
	m = typeText(t, m, "a")
	for i := 0; i < 8; i++ {
		m = newline(t, m)
	}
	m = typeText(t, m, "z") // cursor: row 8, col 1
	if rows := composerRows(m); len(rows) != maxComposerRows {
		t.Fatalf("overflow draft must keep the composer at %d rows, got %d", maxComposerRows, len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "z")
}

// TestComposer_CaretAbsentOnNonComposerSurfaces asserts no hardware caret is
// attached when the Settings surface or the continuation prompt is up — the
// composer is not on screen there (issue #168 scope; full hiding is #169).
func TestComposer_CaretAbsentOnNonComposerSurfaces(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	if c := m.View().Cursor; c != nil {
		t.Errorf("Settings surface must not attach a caret, got %+v", c)
	}
	// Close Settings, then flip into the continuation-prompt state.
	m = asModel(t, mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}))
	m.prompting = true
	if c := m.View().Cursor; c != nil {
		t.Errorf("continuation prompt must not attach a caret, got %+v", c)
	}
}

// TestComposer_CaretHiddenWhileBusy asserts no hardware caret is attached while
// an agent turn is running — the composer is on screen but inert, its keys are
// ignored (ticket #57), so a blinking caret would promise editability the
// surface does not have (issue #169 AC2). The caret returns as soon as the
// turn completes and the composer regains the editing surface (issue #169 AC3).
func TestComposer_CaretHiddenWhileBusy(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	if c := m.View().Cursor; c != nil {
		t.Errorf("agent turn running must not attach a caret, got %+v", c)
	}
	// The turn completes: the composer is the active editing surface again.
	m = mustUpdate(t, m, turnDoneMsg{prompt: "hi", answer: "ok"})
	if c := m.View().Cursor; c == nil {
		t.Error("completing the turn must restore the hardware caret")
	}
}

// TestComposer_CaretHiddenOnReviewPanel asserts no hardware caret is attached
// while the review panel is open — the panel routes keys (up/down/enter), so
// the composer is not editable there (issue #169 AC1). Closing the panel
// restores the caret on the next frame (issue #169 AC3).
func TestComposer_CaretHiddenOnReviewPanel(t *testing.T) {
	var feed = NewToolFeed()
	m := newReviewModel(t, nil)
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/main.go", "edit", "old\n", "new\n", 0, 1)
	m = reopenReview(t, m)
	if m.review == nil {
		t.Fatal("review panel must be open after ctrl+d")
	}
	if c := m.View().Cursor; c != nil {
		t.Errorf("review panel open must not attach a caret, got %+v", c)
	}
	m = reopenReview(t, m)
	if c := m.View().Cursor; c == nil {
		t.Error("closing the review panel must restore the hardware caret")
	}
}

// caretAtEndOfVisibleRow asserts the hardware caret sits right after the last
// character of the composer's visible row that renders needle: the caret's
// column equals that row's plain width and its row equals that row's frame
// row. This pins the caret to the rendered edit line no matter how the
// composer's internal viewport scrolls.
func caretAtEndOfVisibleRow(t *testing.T, m Model, needle string) {
	t.Helper()
	lines := frameLines(m)
	row := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(ansiStrip(lines[i]), needle) {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("no visible composer row contains %q; frame:\n%s", needle, view(m))
	}
	c := caret(t, m)
	plain := strings.TrimRight(ansiStrip(lines[row]), " ")
	if want := plainWidth(plain); c.X != want {
		t.Errorf("caret X = %d, want end of visible row %d (%d); row %q", c.X, want, row, plain)
	}
	if c.Y != row {
		t.Errorf("caret Y = %d, want visible row %d; frame:\n%s", c.Y, row, view(m))
	}
}

// composerRows returns the plain (ANSI-stripped), right-trimmed rows of the
// composer region: the rows after the band's accent separator. The separator
// row is located as the bottom-most row containing ─ — some frames fuse it
// onto the last history row (no trailing newline between regions), others
// render it standalone; both forms are accepted. Draft rows in these tests
// never contain ─.
func composerRows(m Model) []string {
	lines := frameLines(m)
	sep := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(ansiStrip(lines[i]), "─") {
			sep = i
			break
		}
	}
	if sep < 0 {
		return nil
	}
	var rows []string
	for _, l := range lines[sep+1:] {
		rows = append(rows, strings.TrimRight(ansiStrip(l), " "))
	}
	return rows
}

// frameLines returns the frame's rendered rows.
func frameLines(m Model) []string {
	return strings.Split(view(m), "\n")
}

// plainWidth returns the display width of a plain (ANSI-stripped) row.
func plainWidth(s string) int {
	return len([]rune(s))
}
