package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestAnsiStrip_RemovesEscapeSequences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"a\x1b[1;33mb\x1b[0mc", "abc"},
		{"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"\x1b]0;title\x07x", "x"},
		{"a\x1bMb", "ab"},
		{"\x1b[2m dim \x1b[22m", " dim "},
	}
	for _, c := range cases {
		if got := ansiStrip(c.in); got != c.want {
			t.Errorf("ansiStrip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHighlightRange_WrapsOnlySelectedCells(t *testing.T) {
	t.Parallel()
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
		if c.wantKeep != "" && !strings.Contains(got, c.wantKeep) {
			t.Errorf("highlightRange(%q, %d, %d) must keep the line's own escape sequences, got %q", c.line, c.from, c.to, got)
		}
	}
}

func TestHighlightRange_SingleCellAndOutOfRange(t *testing.T) {
	t.Parallel()
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

func historyContentRows(m Model) (rows []string, top int) {
	vp := m.tx.histViewport
	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	for _, l := range strings.Split(hist.String(), "\n") {
		rows = append(rows, ansiStrip(l))
	}
	return rows, vp.YOffset()
}

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

func TestDragSelect_copiesSelectedRange(t *testing.T) {
	t.Parallel()
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

func TestDragSelect_multilineRangeJoinsRows(t *testing.T) {
	t.Parallel()
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

func TestColToRuneIndex_WidthAware(t *testing.T) {
	t.Parallel()
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

func displayCol(row string, b int) int {
	return lipgloss.Width(row[:b])
}

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
	t.Parallel()
	var copied string
	m, _, _, answerRow := newWideAnswerModel(t, &copied)

	m = mustUpdate(t, m, dragMsg("press", 4, answerRow))
	m = mustUpdate(t, m, dragMsg("motion", 9, answerRow))
	if spans := reverseVideoSpans(view(m)); strings.Join(spans, "") != "ab你de" {
		t.Errorf("during-drag highlight spans = %q, want %q", spans, "ab你de")
	}

	mustUpdate(t, m, dragMsg("release", 9, answerRow))
	if copied != "ab你de" {
		t.Errorf("wide-char drag copy = %q, want %q", copied, "ab你de")
	}
}

func TestDragSelect_boundaryInsideWideCharNoPanic(t *testing.T) {
	t.Parallel()
	var copied string
	m, _, _, answerRow := newWideAnswerModel(t, &copied)

	m = mustUpdate(t, m, dragMsg("press", 5, answerRow))
	m = mustUpdate(t, m, dragMsg("motion", 7, answerRow))
	mustUpdate(t, m, dragMsg("release", 7, answerRow))
	if copied != "b你" {
		t.Errorf("boundary-inside-wide-char copy = %q, want %q", copied, "b你")
	}
}

func TestDragSelect_wrappedLinesCopyMatchesDisplay(t *testing.T) {
	t.Parallel()
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

func TestDragSelect_backwardsDragCopiesSameRange(t *testing.T) {
	t.Parallel()
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

	m = mustUpdate(t, m, dragMsg("press", col+len(want)-1, 0))
	m = mustUpdate(t, m, dragMsg("motion", col, 0))
	mustUpdate(t, m, dragMsg("release", col, 0))

	if copied != want {
		t.Errorf("backwards drag copy = %q, want %q", copied, want)
	}
}

func TestDragSelect_highlightsDuringDrag(t *testing.T) {
	t.Parallel()
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
	plain := ansiStrip(content)
	if !strings.Contains(plain, "workspace: /tmp/acme") {
		t.Errorf("highlight must not alter the transcript text, got plain:\n%s", plain)
	}

	m = mustUpdate(t, m, dragMsg("release", col+5, 0))
	if strings.Contains(view(m), "\x1b[7m") {
		t.Errorf("highlight must clear after release, got content:\n%s", view(m))
	}
}

func TestDragSelect_plainClickCopiesNothing(t *testing.T) {
	t.Parallel()
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

func TestDragSelect_ignoresBandAndComposer(t *testing.T) {
	t.Parallel()
	m := dragModel(t, "plain answer")
	bandLines := m.bandHeight()
	m = mustUpdate(t, m, dragMsg("press", 5, m.tx.height-1))
	m = mustUpdate(t, m, dragMsg("motion", 20, m.tx.height-1))
	m = mustUpdate(t, m, dragMsg("release", 20, m.tx.height-1))
	if m.tx.weaver.active {
		t.Errorf("press over the band must not start a selection")
	}

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

func TestDragSelect_scrolledViewportMapsRows(t *testing.T) {
	t.Parallel()
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
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	view(m)
	if m.tx.histViewport.YOffset() <= 0 {
		t.Fatalf("test needs a scrolled viewport, got offset %d", m.tx.histViewport.YOffset())
	}

	rows, top := historyContentRows(m)
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

func TestDragSelect_wheelStillScrollsDuringDrag(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	rows, top := historyContentRows(m)
	if top <= 0 {
		t.Fatalf("overflowed follow should be scrolled to the bottom, got top %d", top)
	}
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
	if !m.tx.weaver.active {
		t.Errorf("wheel must not cancel the in-progress drag")
	}
	m = mustUpdate(t, m, dragMsg("release", col+3, screenRow))
	if row == "" {
		t.Errorf("test assumption broken: first visible row should have text")
	}
}

func TestClickToExpand_togglesToolEntry(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
		Config: config.Config{CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true},
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
		if strings.Contains(r, "🔧 bash") {
			headRow = i
			break
		}
	}
	if headRow < 0 {
		t.Fatalf("tool head row not found, got %q", rows)
	}
	if idx, _, ok := m.tx.toolEntryAtLine(headRow); !ok || idx != 0 {
		t.Fatalf("toolEntryAtLine(%d) = %d/%v, want entry 0", headRow, idx, ok)
	}

	m = mustUpdate(t, m, dragMsg("press", 2, headRow))
	m = mustUpdate(t, m, dragMsg("release", 2, headRow))
	if !strings.Contains(view(m), "full output line one") {
		t.Errorf("click must expand the entry, got: %q", view(m))
	}
	if m.tx.expandAll {
		t.Error("click must not set the global expandAll flag")
	}

	m = mustUpdate(t, m, dragMsg("press", 2, headRow))
	m = mustUpdate(t, m, dragMsg("release", 2, headRow))
	if strings.Contains(view(m), "full output line one") {
		t.Errorf("second click must collapse the entry, got: %q", view(m))
	}

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
