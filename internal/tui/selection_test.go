package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	vp := m.tx.histViewport
	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	// Drag from mid startRow to mid endRow in display-column space; the
	// expected text is derived from the visible surface alone (issue #261:
	// width-aware, rune-safe, so it is read via the display cells and sliced by
	// runes).
	startCol := 3
	endCol := 5
	var want string
	if startRow == endRow {
		want = runeRangeFromDisplay(rows[startRow], startCol, endCol)
	} else {
		want = runeRangeFromDisplay(rows[startRow], startCol, -1) + "\n" +
			strings.Join(rows[startRow+1:endRow], "\n") + "\n" +
			runeRangeFromDisplay(rows[endRow], 0, endCol)
	}

	m = mustUpdate(t, m, dragMsg("press", startCol, startRow))
	m = mustUpdate(t, m, dragMsg("motion", endCol, endRow))
	mustUpdate(t, m, dragMsg("release", endCol, endRow))

	if copied != want {
		t.Errorf("multi-line drag copy = %q, want %q", copied, want)
	}
}

// --- Slice B: width-aware, rune-safe selection (issue #261) -----------------

// TestColToRuneIndex_WidthAware asserts colToRuneIndex maps a display-width
// column (the mouse cell space) to a rune index into the plain line, so wide
// characters (CJK = 2 display cells, 1 rune) never misalign the selection. A
// column that lands on a wide rune's second display cell maps to that same
// rune; a column past the end of the line clamps to the last rune. Hand-worked
// widths: in "ab你defg" 你 occupies display columns 2-3 and is 1 rune (issue
// #261 width/run mismatch repro).
func TestColToRuneIndex_WidthAware(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want int
	}{
		{"", 0, 0},
		{"abc", 0, 0},
		{"abc", 2, 2},
		{"abc", 5, 2}, // past end clamps to last rune
		{"ab你defg", 0, 0},
		{"ab你defg", 1, 1},
		{"ab你defg", 2, 2}, // 你 first cell
		{"ab你defg", 3, 2}, // 你 second cell still maps to 你
		{"ab你defg", 4, 3}, // 'd'
		{"ab你defg", 7, 6}, // 'g' (last rune)
		{"ab你defg", 9, 6}, // past end clamps to last rune
	}
	for _, c := range cases {
		if got := colToRuneIndex(c.line, c.col); got != c.want {
			t.Errorf("colToRuneIndex(%q, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
}

// displayCol returns the display-width column of the byte index b within row.
// Rows may carry multibyte prefix glyphs (e.g. the │ separator), whose display
// width differs from their byte offset, so drag tests must convert byte
// offsets to display columns before treating them as mouse X (issue #261).
func displayCol(row string, b int) int {
	return lipgloss.Width(row[:b])
}

// runeRangeFromDisplay returns the plain runes of row covering the inclusive
// display-cell range [fromDisp, toDisp]; a negative toDisp means through the
// end of the row. Drag tests derive the expected copy from the visible display
// surface this way (issue #261: width-aware, rune-safe).
func runeRangeFromDisplay(row string, fromDisp, toDisp int) string {
	rs := []rune(row)
	if len(rs) == 0 {
		return ""
	}
	s := colToRuneIndex(row, fromDisp)
	if s > len(rs)-1 {
		return ""
	}
	e := len(rs) - 1
	if toDisp >= 0 {
		e = colToRuneIndex(row, toDisp)
		if e > len(rs)-1 {
			e = len(rs) - 1
		}
	}
	if s > e {
		return ""
	}
	return string(rs[s : e+1])
}

// reverseVideoSpans returns the plain text of every contiguous reverse-video
// (SGR 7) run in s, in order — a way for tests to assert a drag highlights
// exactly the intended runes and no others (issue #261: no under-coverage, no
// leak into neighbouring rows).
func reverseVideoSpans(s string) []string {
	rs := []rune(s)
	var spans []string
	var b strings.Builder
	in := false
	i := 0
	for i < len(rs) {
		if rs[i] == '\x1b' {
			n := consumeEscape(rs, i)
			seq := string(rs[i : i+n])
			if seq == "\x1b[7m" {
				in = true
				b.Reset()
			} else if seq == "\x1b[27m" {
				spans = append(spans, b.String())
				in = false
			}
			i += n
			continue
		}
		if in {
			b.WriteRune(rs[i])
		}
		i++
	}
	return spans
}

// TestDragSelect_wideCharCopyMatchesHighlight is the issue #261 regression: a
// drag over a transcript row containing a wide CJK character must copy exactly
// the marked runes and highlight exactly the cells they cover. The answer row
// renders as "│   ab你defg": 4 ASCII display cells of prefix, then the answer
// where 你 occupies two display cells but is one rune. Dragging display columns
// 4..9 covers runes [4,8] = "ab你de" (a, b, 你, d, e).
// newWideAnswerModel builds a model whose transcript answer row is the wide/CJK
// string "ab你defg" (issue #261), wires the clipboard to record into *copied, and
// returns the model plus the answer row's cell coordinates ready for a drag.
func newWideAnswerModel(t *testing.T, copied *string) (m Model, rows []string, top, answerRow int) {
	m = NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ab你defg"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { *copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top = historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	answerRow = -1
	for i, r := range rows {
		if strings.Contains(r, "ab你defg") {
			answerRow = i
			break
		}
	}
	if answerRow < 0 {
		t.Fatalf("answer row not found, got %q", rows)
	}
	return m, rows, top, answerRow
}

func TestDragSelect_wideCharCopyMatchesHighlight(t *testing.T) {
	var copied string
	m, _, _, answerRow := newWideAnswerModel(t, &copied)

	// Reverse-video must wrap exactly "ab你de" (runes [4,8]) while the drag is
	// in progress.
	m = mustUpdate(t, m, dragMsg("press", 4, answerRow))
	m = mustUpdate(t, m, dragMsg("motion", 9, answerRow))
	if spans := reverseVideoSpans(view(m)); strings.Join(spans, "") != "ab你de" {
		t.Errorf("during-drag highlight spans = %q, want %q", spans, "ab你de")
	}

	m = mustUpdate(t, m, dragMsg("release", 9, answerRow))
	if copied != "ab你de" {
		t.Errorf("wide-char drag copy = %q, want %q", copied, "ab你de")
	}
}

// TestDragSelect_boundaryInsideWideCharNoPanic asserts selecting a range whose
// end display column lands inside a multibyte CJK rune neither panics nor
// corrupts the copy: dragging display column 5..7 (b through 你's two cells)
// copies runes [5,6] = "b你" intact.
func TestDragSelect_boundaryInsideWideCharNoPanic(t *testing.T) {
	var copied string
	m, _, _, answerRow := newWideAnswerModel(t, &copied)

	m = mustUpdate(t, m, dragMsg("press", 5, answerRow))
	m = mustUpdate(t, m, dragMsg("motion", 7, answerRow))
	m = mustUpdate(t, m, dragMsg("release", 7, answerRow))
	if copied != "b你" {
		t.Errorf("boundary-inside-wide-char copy = %q, want %q", copied, "b你")
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	// The mouse reports display-width columns; convert the byte offsets and
	// derive the expected copy from the visible display cells (issue #261:
	// width-aware, rune-safe).
	firstDisp := displayCol(rows[first], c0)
	secondEndDisp := displayCol(rows[second], c1)
	want := runeRangeFromDisplay(rows[first], firstDisp, -1) + "\n" +
		runeRangeFromDisplay(rows[second], 0, secondEndDisp-1)

	m = mustUpdate(t, m, dragMsg("press", firstDisp, first))
	m = mustUpdate(t, m, dragMsg("motion", secondEndDisp-1, second))
	mustUpdate(t, m, dragMsg("release", secondEndDisp-1, second))

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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	mustUpdate(t, m, dragMsg("release", col, 0))

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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	mustUpdate(t, m, dragMsg("release", 2, 0))

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
	m = mustUpdate(t, m, dragMsg("press", 5, m.tx.height-1))
	m = mustUpdate(t, m, dragMsg("motion", 20, m.tx.height-1))
	m = mustUpdate(t, m, dragMsg("release", 20, m.tx.height-1))
	if m.tx.dragSel != nil {
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	if m.tx.histViewport.YOffset() <= 0 {
		t.Fatalf("test needs a scrolled viewport, got offset %d", m.tx.histViewport.YOffset())
	}

	rows, top := historyContentRows(m)
	// Pick a visible row that carries answer text.
	target := -1
	for i := top; i < top+m.tx.histViewport.Height() && i < len(rows); i++ {
		if strings.Contains(rows[i], "answer") && strings.TrimSpace(rows[i]) != "" {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("no visible answer row to select (top=%d, rows=%q)", top, rows)
	}
	col := strings.Index(rows[target], "answer")
	// The mouse X is a display-width column; the byte offset differs for rows
	// with multibyte prefix glyphs (issue #261). "answer" is pure ASCII, so
	// its display width equals its rune count.
	disp := displayCol(rows[target], col)
	want := "answer"
	screenRow := target - top

	m = mustUpdate(t, m, dragMsg("press", disp, screenRow))
	m = mustUpdate(t, m, dragMsg("motion", disp+len("answer")-1, screenRow))
	m = mustUpdate(t, m, dragMsg("release", disp+len("answer")-1, screenRow))

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
	before := m.tx.histViewport.YOffset()

	m = mustUpdate(t, m, wheelMsg(true)) // wheel up
	if got := m.tx.histViewport.YOffset(); got >= before {
		t.Errorf("wheel during drag must still scroll up: offset %d -> %d", before, got)
	}
	if m.tx.dragSel == nil {
		t.Errorf("wheel must not cancel the in-progress drag")
	}
	m = mustUpdate(t, m, dragMsg("release", col+3, screenRow))
	if row == "" {
		t.Errorf("test assumption broken: first visible row should have text")
	}
}

// TestClickToExpand_togglesToolEntry asserts a plain mouse click (press +
// release on one cell, no drag) on a collapsed tool entry toggles just that
// entry open, and a second click collapses it — while clicks on non-tool rows
// stay inert and the global expandAll flag is never touched (benchmark §4.4
// mouse ergonomics: click-to-expand tool results).
func TestClickToExpand_togglesToolEntry(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one\nfull output line two", Lines: 2})
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	headRow := -1
	for i, r := range rows {
		if strings.Contains(r, "⊕ bash") {
			headRow = i
			break
		}
	}
	if headRow < 0 {
		t.Fatalf("tool head row not found, got %q", rows)
	}
	// The row accounting must map the head row to this tool's entry.
	if idx, _, ok := m.tx.toolEntryAtLine(headRow); !ok || idx != 0 {
		t.Fatalf("toolEntryAtLine(%d) = %d/%v, want entry 0", headRow, idx, ok)
	}

	// Click on the head row: press + release on one cell, no drag.
	m = mustUpdate(t, m, dragMsg("press", 2, headRow))
	m = mustUpdate(t, m, dragMsg("release", 2, headRow))
	if !strings.Contains(view(m), "full output line one") {
		t.Errorf("click must expand the entry, got: %q", view(m))
	}
	if m.tx.expandAll {
		t.Error("click must not set the global expandAll flag")
	}

	// Second click collapses it again.
	m = mustUpdate(t, m, dragMsg("press", 2, headRow))
	m = mustUpdate(t, m, dragMsg("release", 2, headRow))
	if strings.Contains(view(m), "full output line one") {
		t.Errorf("second click must collapse the entry, got: %q", view(m))
	}

	// Click on a non-tool row (the prompt row) stays inert.
	promptRow := -1
	for i, r := range rows {
		if strings.Contains(r, "run it") {
			promptRow = i
			break
		}
	}
	if promptRow < 0 {
		t.Fatalf("prompt row not found, got %q", rows)
	}
	m = mustUpdate(t, m, dragMsg("press", 2, promptRow))
	m = mustUpdate(t, m, dragMsg("release", 2, promptRow))
	if strings.Contains(view(m), "full output line one") {
		t.Errorf("click off a tool row must not expand anything, got: %q", view(m))
	}
}
