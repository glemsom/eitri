package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func railBandModel(t *testing.T, w, h int) Model {
	t.Helper()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, nm)
}

func bandRowsFrom(plain string) (sep int, rows []string) {
	lines := strings.Split(plain, "\n")
	sep = -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Trim(trimmed, "─") != "" {
			continue
		}
		if i+1 < len(lines) && strings.Contains(lines[i+1], "Ask Eitri") {
			sep = i
			break
		}
	}
	if sep < 0 {
		return -1, nil
	}
	return sep, lines[sep:]
}

func TestModelBandSpansFullTerminalWidthTall(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 40)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x40")
	}
	if bw, tw := m.tx.bandWidth(), m.tx.transcriptWidth(); bw <= tw {
		t.Errorf("bandWidth = %d must exceed rail-shrunk transcriptWidth = %d (band spans full terminal width, history stays rail-shrunk)", bw, tw)
	}
	if w := m.tx.bandWidth(); w != 120-2 {
		t.Errorf("bandWidth = %d, want full terminal width minus gutter = %d", w, 120-2)
	}

	plain := plain(view(m))
	sep, band := bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}

	for i, r := range band {
		if w := plainWidth(r); w != 120-2 {
			t.Errorf("band row %d (frame row %d) is %d wide, want full terminal width %d (blank corner must be gone)", i, sep+i, w, 120-2)
		}
	}
}

func TestModelHistoryWrapsAtTranscriptWidthWithRail(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 40)
	fill := strings.Repeat("a", 40)
	long := fill + " " + fill + " XYZEND" // tail beyond transcriptWidth content, within full band
	m = typeText(t, m, long)
	m = submitAndWait(t, m)

	p := plain(view(m))
	if !strings.Contains(p, "XYZEND") {
		t.Errorf(
			"history wrapped at the full band width, truncating the prompt tail %q behind the rail (transcriptWidth=%d bandWidth=%d); must wrap at transcriptWidth",
			"XYZEND", m.tx.transcriptWidth(), m.tx.bandWidth(),
		)
	}
	for _, ln := range strings.Split(p, "\n") {
		if i := strings.Index(ln, "XYZEND"); i >= 0 && i >= m.tx.transcriptWidth() {
			t.Errorf("history tail column %d not below transcriptWidth=%d (rail-shrunk wrap width)", i, m.tx.transcriptWidth())
		}
	}
}

func TestModelRailEndsOneRowAboveBand(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 10)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x10")
	}

	plain := plain(view(m))
	sep, _ := bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}

	lastRail := -1
	for i, ln := range strings.Split(plain, "\n") {
		if i >= sep {
			break
		}
		if strings.Contains(ln, "│") {
			lastRail = i
		}
	}
	if lastRail < 0 {
		t.Fatalf("no rail border in frame:\n%s", plain)
	}
	if want := sep - 1; lastRail != want {
		t.Errorf("rail's last border row = %d, want exactly one row above the band top at row %d", lastRail, want)
	}
	if lastRail >= sep {
		t.Error("rail overlaps the band")
	}
}

func TestModelRailEndsOneRowAboveBandTall(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 40)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x40")
	}

	plain := plain(view(m))
	sep, _ := bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}

	lastRail := -1
	for i, ln := range strings.Split(plain, "\n") {
		if i >= sep {
			break
		}
		if strings.Contains(ln, "│") {
			lastRail = i
		}
	}
	if lastRail < 0 {
		t.Fatalf("no rail border row in frame:\n%s", plain)
	}
	if want := sep - 1; lastRail != want {
		t.Errorf("rail's last border row = %d, want exactly one row above the band top at row %d (rail must fill down to the band at every height)", lastRail, want)
	}
	if lastRail >= sep {
		t.Error("rail overlaps the band")
	}
}

func TestModelComposerCaretStaysCorrectWithRail(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 40)
	composerTop := lineCount(view(m)) - minComposerRows - 2

	c := m.View().Cursor
	if c == nil {
		t.Fatal("hardware caret must be attached while the composer is the active surface")
	}
	if c.X != 3 || c.Y != composerTop {
		t.Errorf("empty-composer caret with rail = (%d,%d), want (3,%d)", c.X, c.Y, composerTop)
	}

	m = typeText(t, m, "hi")
	after := caret(t, m)
	if after.X != 5 || after.Y != composerTop {
		t.Errorf("caret after typing %q with rail = (%d,%d), want (5,%d)", "hi", after.X, after.Y, composerTop)
	}
}

func TestModelComposerCaretStaysCorrectWithRailWrapped(t *testing.T) {
	t.Parallel()
	m := railBandModel(t, 120, 40)
	m = typeText(t, m, strings.Repeat("a", 100)) // wraps at the full-width composer
	if rows := composerRows(m); len(rows) < 2 {
		t.Fatalf("draft must wrap to at least two composer rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "a")
}

var tallBandHeights = []int{26, 30, 35, 40, 50}

func rowRole(i int) string {
	switch i {
	case 0:
		return "separator"
	case 1:
		return "status strip"
	default:
		return "composer"
	}
}

func bandRowsForHeight(t *testing.T, h int) (sep int, rows []string) {
	t.Helper()
	m := railBandModel(t, 120, h)
	if !m.tx.railVisible() {
		t.Fatalf("rail must stay visible at 120x%d", h)
	}
	plain := plain(view(m))
	sep, rows = bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}
	if len(rows) < 3 {
		t.Fatalf("band has %d rows, want separator+status+composer >= 3:\n%s", len(rows), plain)
	}
	return sep, rows
}

func TestModelBandSpansFullWidthUnderRailTallSweep(t *testing.T) {
	t.Parallel()
	for _, h := range tallBandHeights {
		h := h
		t.Run(fmt.Sprintf("height/%d", h), func(t *testing.T) {
			m := railBandModel(t, 120, h)
			if !m.tx.railVisible() {
				t.Fatal("rail must stay visible at 120x", h)
			}
			if bw, tw := m.tx.bandWidth(), m.tx.transcriptWidth(); bw <= tw {
				t.Errorf("h=%d bandWidth=%d must exceed rail-shrunk transcriptWidth=%d across the tall range", h, bw, tw)
			}
			if w := m.tx.bandWidth(); w != 120-2 {
				t.Errorf("h=%d bandWidth=%d, want full terminal width minus gutter=%d", h, w, 120-2)
			}

			sep, rows := bandRowsForHeight(t, h)
			want := 120 - 2
			for i, r := range rows {
				if got := plainWidth(r); got != want {
					t.Errorf("h=%d %s row (frame row %d) is %d wide, want full terminal width %d (dead corner under rail must be gone)", h, rowRole(i), sep+i, got, want)
				}
			}
		})
	}
}

func TestModelRailEndsOneRowAboveBandTallSweep(t *testing.T) {
	t.Parallel()
	for _, h := range tallBandHeights {
		h := h
		t.Run(fmt.Sprintf("height/%d", h), func(t *testing.T) {
			m := railBandModel(t, 120, h)
			if !m.tx.railVisible() {
				t.Fatal("rail must stay visible at 120x", h)
			}
			sep, _ := bandRowsForHeight(t, h)
			framePlain := plain(view(m))
			lastRail := -1
			for i, ln := range strings.Split(framePlain, "\n") {
				if i >= sep {
					break
				}
				if strings.Contains(ln, "│") {
					lastRail = i
				}
			}
			if lastRail < 0 {
				t.Fatalf("no rail border row in frame at 120x%d", h)
			}
			if want := sep - 1; lastRail != want {
				t.Errorf("h=%d rail's last border row=%d, want exactly one row above the band top at row %d (rail must fill down to the band at every tall height)", h, lastRail, want)
			}
			if lastRail >= sep {
				t.Errorf("h=%d rail overlaps the band", h)
			}
		})
	}
}
