package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// This file covers the T6 drag-select copy seam (issue #124): a click-drag
// over the history viewport highlights a cell range, and releasing the drag
// copies the selected plain-text range to the clipboard through the same seam
// as Ctrl+O and /copy (issue #123). Selection is hand-rolled from raw mouse
// cell state over the wrapped-lines transcript — no bubbles v2 upgrade. Tests
// drive the tui.Model Update seam with tea.MouseMsg events and assert on the
// clipboard seam and the rendered View(), never on internal caches.

// --- Slice A: hand-rolled ANSI cell helpers ---------------------------------

// TestAnsiStrip_RemovesEscapeSequences asserts ansiStrip removes CSI, OSC, and
// two-character escape sequences while keeping every printable rune, so the
// plain-text cell grid selection maps into matches the rendered transcript.
func TestAnsiStrip_RemovesEscapeSequences(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain", "plain"},
		// CSI SGR sequences (colors, styles).
		{"\x1b[31mred\x1b[0m", "red"},
		{"a\x1b[1;33mb\x1b[0mc", "abc"},
		// OSC (hyperlink / title), terminated by BEL or ST.
		{"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"\x1b]0;title\x07x", "x"},
		// Two-character escapes (ESC M, ESC 7/8) are dropped whole.
		{"a\x1bMb", "ab"},
		// ANSI adjacent to plain text keeps order.
		{"\x1b[2m dim \x1b[22m", " dim "},
	}
	for _, c := range cases {
		if got := ansiStrip(c.in); got != c.want {
			t.Errorf("ansiStrip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHighlightRange_WrapsOnlySelectedCells asserts highlightRange wraps the
// plain cells [from,to] of an ANSI-styled line in reverse-video escapes while
// preserving every original byte outside the range: cells before the range,
// after it, and all escape sequences keep their exact bytes, so the surrounding
// styling never breaks (issue #124 AC1, no partial-code artifacts).
func TestHighlightRange_WrapsOnlySelectedCells(t *testing.T) {
	cases := []struct {
		line       string
		from, to   int
		wantPlain  string
		wantKeep   string // an original escape sequence that must survive verbatim
		wantHasRev bool
	}{
		{"abcdef", 1, 3, "abcdef", "", true},
		{"\x1b[31mred\x1b[0m", 1, 2, "red", "\x1b[31m", true},
		{"abcdef", 0, 5, "abcdef", "", true},
	}
	for _, c := range cases {
		got := highlightRange(c.line, c.from, c.to)
		if plain := ansiStrip(got); plain != c.wantPlain {
			t.Errorf("highlightRange(%q, %d, %d) plain = %q, want %q", c.line, c.from, c.to, plain, c.wantPlain)
		}
		if !c.wantHasRev {
			continue
		}
		if !strings.Contains(got, "\x1b[7m") {
			t.Errorf("highlightRange(%q, %d, %d) missing reverse-video on, got %q", c.line, c.from, c.to, got)
		}
		if !strings.Contains(got, "\x1b[27m") {
			t.Errorf("highlightRange(%q, %d, %d) missing reverse-video off, got %q", c.line, c.from, c.to, got)
		}
		// The escape sequences belonging to the line survive verbatim.
		if c.wantKeep != "" && !strings.Contains(got, c.wantKeep) {
			t.Errorf("highlightRange(%q, %d, %d) must keep the line's own escape sequences, got %q", c.line, c.from, c.to, got)
		}
	}
}

// TestHighlightRange_SingleCellAndOutOfRange asserts a one-cell range still
// turns reverse video on and off around that single rune, and a range past the
// end of the line leaves the line unchanged (no panic, no spurious markers).
func TestHighlightRange_SingleCellAndOutOfRange(t *testing.T) {
	got := highlightRange("ab", 1, 1)
	if plain := ansiStrip(got); plain != "ab" {
		t.Errorf("single-cell highlight plain = %q, want \"ab\"", plain)
	}
	if !strings.Contains(got, "\x1b[7m") || !strings.Contains(got, "\x1b[27m") {
		t.Errorf("single-cell highlight missing reverse markers, got %q", got)
	}
	if got := highlightRange("ab", 5, 9); got != "ab" {
		t.Errorf("out-of-range highlight must be a no-op, got %q", got)
	}
	if got := highlightRange("ab", 2, 1); got != "ab" {
		t.Errorf("inverted range highlight must be a no-op, got %q", got)
	}
}

// --- Model-level: drag select through the Update/View seam ------------------

// dragModel builds a small model whose whole history fits the viewport: a
// workspace header plus one turn, so the viewport follows with offset 0 and
// every content row is a known screen row. Rendered once so the persisted
// viewport is hydrated before mouse events land.
func dragModel(t *testing.T, answer string) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: answer}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(string) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m) // hydrate the persisted viewport with current content
	return m
}

// historyContentRows returns the plain text of each row of the rendered
// history content (the coordinate space selection maps into), plus the
// viewport's top content row so tests can convert a content row to a screen
// row. The content rows are unpadded — the viewport pads rows to the terminal
// width only when rendering — so they are the exact text a selection copies.
func historyContentRows(m Model) (rows []string, top int) {
	vp := m.histViewport
	var hist strings.Builder
	m.renderHistory(&hist)
	for _, l := range strings.Split(hist.String(), "\n") {
		rows = append(rows, ansiStrip(l))
	}
	return rows, vp.YOffset()
}

// dragMsg builds a mouse event for the drag-select seam (issue #124) in
// bubbletea v2's per-type mouse message shape (pass 2, issue #146): a left
// press becomes a MouseClickMsg, motion a MouseMotionMsg, and release a
// MouseReleaseMsg.
func dragMsg(action string, x, y int) tea.Msg {
	switch action {
	case "press":
		return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
	case "motion":
		return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y}
	default:
		return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}
	}
}

// TestDragSelect_copiesSelectedRange asserts a click-drag over a transcript
// row copies exactly the selected plain cells to the clipboard on release
// (issue #124 AC2): press anchors the range, motion extends it, release copies
// through the same seam as Ctrl+O, and the band reports the copy.
func TestDragSelect_copiesSelectedRange(t *testing.T) {
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d (rows: %q)", top, rows)
	}
	row := rows[0]
	col := strings.Index(row, "workspace")
	if col < 0 {
		t.Fatalf("workspace header not on row 0, got %q", row)
	}
	want := "workspace"

	m = mustUpdate(t, m, dragMsg("press", col, 0))
	m = mustUpdate(t, m, dragMsg("motion", col+len(want)-1, 0))
	m = mustUpdate(t, m, dragMsg("release", col+len(want)-1, 0))

	if copied != want {
		t.Errorf("drag copy = %q, want %q", copied, want)
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

// TestDragSelect_multilineRangeJoinsRows asserts a drag spanning two rows
// copies the visible plain slices joined by a newline — the wrapped-lines
// behaviour (issue #124 AC3): the copied snippet is exactly the characters
// shown on screen for the selected cells, with no escape residue.
func TestDragSelect_multilineRangeJoinsRows(t *testing.T) {
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	// Locate the prompt row and the answer row on the visible surface; the
	// drag spans the rows between them (blank separator rows included).
	startRow, endRow := -1, -1
	for i, r := range rows {
		if startRow < 0 && strings.Contains(r, "hi") {
			startRow = i
		}
		if strings.Contains(r, "plain") {
			endRow = i
		}
	}
	if startRow < 0 || endRow < 0 || endRow < startRow {
		t.Fatalf("need prompt and answer rows, got %q", rows)
	}
	// Drag from mid startRow to mid endRow; the expected text is derived from
	// the visible surface alone.
	startCol := 3
	endCol := 5
	var want string
	if startRow == endRow {
		want = rows[startRow][startCol : endCol+1]
	} else {
		want = rows[startRow][startCol:] + "\n" + strings.Join(rows[startRow+1:endRow], "\n") + "\n" + rows[endRow][:endCol+1]
	}

	m = mustUpdate(t, m, dragMsg("press", startCol, startRow))
	m = mustUpdate(t, m, dragMsg("motion", endCol, endRow))
	m = mustUpdate(t, m, dragMsg("release", endCol, endRow))

	if copied != want {
		t.Errorf("multi-line drag copy = %q, want %q", copied, want)
	}
}

// TestDragSelect_wrappedLinesCopyMatchesDisplay asserts a drag across a
// soft-wrapped transcript row copies the per-row slices joined with newlines,
// reproducing exactly the wrapped rows the user saw (issue #124 AC3: no
// partial-code artifacts from wrapped lines or ANSI styling).
func TestDragSelect_wrappedLinesCopyMatchesDisplay(t *testing.T) {
	var copied string
	long := strings.Repeat("word ", 40) // wraps across several display rows
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: long}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	// Locate two consecutive rows of the wrapped answer (both contain "word").
	first, second := -1, -1
	for i := 0; i+1 < len(rows); i++ {
		if strings.Contains(rows[i], "word") && strings.Contains(rows[i+1], "word") {
			first, second = i, i+1
			break
		}
	}
	if first < 0 {
		t.Fatalf("answer did not wrap across rows, got %q", rows)
	}
	c0 := strings.Index(rows[first], "word")
	c1 := strings.Index(rows[second], "word") + len("word")
	want := rows[first][c0:] + "\n" + rows[second][:c1]

	m = mustUpdate(t, m, dragMsg("press", c0, first))
	m = mustUpdate(t, m, dragMsg("motion", c1-1, second))
	m = mustUpdate(t, m, dragMsg("release", c1-1, second))

	if copied != want {
		t.Errorf("wrapped drag copy = %q, want %q", copied, want)
	}
	if strings.Contains(copied, "\x1b[") {
		t.Errorf("drag copy must be ANSI-free, got: %q", copied)
	}
}

// TestDragSelect_backwardsDragCopiesSameRange asserts dragging from the end
// cell back to the start cell copies the same normalized range as the forward
// drag (issue #124 AC2: selection is direction-independent).
func TestDragSelect_backwardsDragCopiesSameRange(t *testing.T) {
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	col := strings.Index(rows[0], "workspace")
	want := "workspace"

	// Press at the end, drag back to the start.
	m = mustUpdate(t, m, dragMsg("press", col+len(want)-1, 0))
	m = mustUpdate(t, m, dragMsg("motion", col, 0))
	m = mustUpdate(t, m, dragMsg("release", col, 0))

	if copied != want {
		t.Errorf("backwards drag copy = %q, want %q", copied, want)
	}
}

// TestDragSelect_highlightsDuringDrag asserts the dragged cell range renders
// highlighted (reverse video) while the drag is in progress, and the highlight
// is gone after release (issue #124 AC1). The whole surface is scanned for
// the reverse-video marker: the composer paints no software caret cell
// anymore, so no row needs excluding (issue #168).
func TestDragSelect_highlightsDuringDrag(t *testing.T) {
	m := dragModel(t, "plain answer")
	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	col := strings.Index(rows[0], "workspace")

	m = mustUpdate(t, m, dragMsg("press", col, 0))
	m = mustUpdate(t, m, dragMsg("motion", col+5, 0))

	content := view(m)
	if !strings.Contains(content, "\x1b[7m") {
		t.Errorf("drag in progress must highlight the range in reverse video, got content:\n%s", content)
	}
	// The plain transcript text survives the highlight intact.
	plain := ansiStrip(content)
	if !strings.Contains(plain, "workspace: /tmp/acme") {
		t.Errorf("highlight must not alter the transcript text, got plain:\n%s", plain)
	}

	m = mustUpdate(t, m, dragMsg("release", col+5, 0))
	if strings.Contains(view(m), "\x1b[7m") {
		t.Errorf("highlight must clear after release, got content:\n%s", view(m))
	}
}

// TestDragSelect_plainClickCopiesNothing asserts a press+release on one cell
// (no drag) never reaches the clipboard — only an actual drag copies (issue
// #124 AC2: click-dragging, not clicking).
func TestDragSelect_plainClickCopiesNothing(t *testing.T) {
	copied := ""
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	m = mustUpdate(t, m, dragMsg("press", 2, 0))
	m = mustUpdate(t, m, dragMsg("release", 2, 0))

	if copied != "" {
		t.Errorf("plain click must not copy, got %q", copied)
	}
}

// TestDragSelect_ignoresBandAndComposer asserts a press over the fixed bottom
// band never starts a selection and drag events never disturb the composer
// input (issue #124 AC4: selection does not interfere with composer input).
func TestDragSelect_ignoresBandAndComposer(t *testing.T) {
	m := dragModel(t, "plain answer")
	bandLines := m.bandHeight()
	// A press on the band's own row (last terminal row).
	m = mustUpdate(t, m, dragMsg("press", 5, m.height-1))
	m = mustUpdate(t, m, dragMsg("motion", 20, m.height-1))
	m = mustUpdate(t, m, dragMsg("release", 20, m.height-1))
	if m.dragSel != nil {
		t.Errorf("press over the band must not start a selection")
	}

	// Drag events must never mutate the composer.
	before := m.composer.Value()
	m = mustUpdate(t, m, dragMsg("press", 2, 0))
	m = mustUpdate(t, m, dragMsg("motion", 8, 0))
	m = mustUpdate(t, m, dragMsg("release", 8, 0))
	if got := m.composer.Value(); got != before {
		t.Errorf("drag must not touch the composer: %q -> %q", before, got)
	}
	if bandLines <= 0 {
		t.Errorf("test assumption broken: band should occupy rows")
	}
}

// TestDragSelect_scrolledViewportMapsRows asserts the screen-to-content mapping
// holds after the user scrolls up: a drag over a visible row copies that row's
// plain text even though it is no longer the first content row (issue #124
// AC3 — selection reads the rendered transcript, not a fixed offset).
func TestDragSelect_scrolledViewportMapsRows(t *testing.T) {
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "answer " + prompt}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
		Clipboard: func(s string) error { copied = s; return nil },
	})
	for i := 1; i <= 5; i++ {
		m = typeText(t, m, "q"+string(rune('a'+i-1)))
		m = submitAndWait(t, m)
	}
	m = resizeTo(t, m, 120, 12)
	view(m) // hydrate
	// Scroll up once so follow breaks and the viewport holds an offset.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	view(m)
	if m.histViewport.YOffset() <= 0 {
		t.Fatalf("test needs a scrolled viewport, got offset %d", m.histViewport.YOffset())
	}

	rows, top := historyContentRows(m)
	// Pick a visible row that carries answer text.
	target := -1
	for i := top; i < top+m.histViewport.Height() && i < len(rows); i++ {
		if strings.Contains(rows[i], "answer") && strings.TrimSpace(rows[i]) != "" {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("no visible answer row to select (top=%d, rows=%q)", top, rows)
	}
	col := strings.Index(rows[target], "answer")
	want := rows[target][col : col+len("answer")]
	screenRow := target - top

	m = mustUpdate(t, m, dragMsg("press", col, screenRow))
	m = mustUpdate(t, m, dragMsg("motion", col+len("answer")-1, screenRow))
	m = mustUpdate(t, m, dragMsg("release", col+len("answer")-1, screenRow))

	if copied != want {
		t.Errorf("scrolled drag copy = %q, want %q (screen row %d, content row %d)", copied, want, screenRow, target)
	}
}

// TestDragSelect_wheelStillScrollsDuringDrag asserts an in-progress drag does
// not swallow wheel scrolling: the wheel moves the viewport while a selection
// is being drawn, so selection never interferes with scroll navigation (issue
// #124 AC4).
func TestDragSelect_wheelStillScrollsDuringDrag(t *testing.T) {
	m := scrollOverflowModel(t)
	rows, top := historyContentRows(m)
	if top <= 0 {
		t.Fatalf("overflowed follow should be scrolled to the bottom, got top %d", top)
	}
	// The first visible row can be a blank separator; anchor the drag on the
	// first non-blank visible row so the press starts a real selection.
	screenRow := 0
	for top+screenRow < len(rows) && strings.TrimSpace(rows[top+screenRow]) == "" {
		screenRow++
	}
	if top+screenRow >= len(rows) {
		t.Fatalf("no non-blank visible row, got rows %q", rows)
	}
	row := rows[top+screenRow]
	col := 0

	m = mustUpdate(t, m, dragMsg("press", col, screenRow))
	m = mustUpdate(t, m, dragMsg("motion", col+3, screenRow))
	before := m.histViewport.YOffset()

	m = mustUpdate(t, m, wheelMsg(true)) // wheel up
	if got := m.histViewport.YOffset(); got >= before {
		t.Errorf("wheel during drag must still scroll up: offset %d -> %d", before, got)
	}
	if m.dragSel == nil {
		t.Errorf("wheel must not cancel the in-progress drag")
	}
	m = mustUpdate(t, m, dragMsg("release", col+3, screenRow))
	if row == "" {
		t.Errorf("test assumption broken: first visible row should have text")
	}
}
