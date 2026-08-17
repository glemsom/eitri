package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// railBandModel builds a sized chat model wired with both a telemetry strip
// (so the band carries a status row) and the right context rail, at the given
// terminal dimensions. It is the fixture for the edge-to-edge bottom
// band tests.
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

// bandRowsFrom splits the plain frame into the band's row range: from the
// bottom-most accent separator row (the band's top edge) down. It returns the
// separator row index and the band row strings (separator inclusive).
func bandRowsFrom(plain string) (sep int, rows []string) {
	lines := strings.Split(plain, "\n")
	sep = -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "─") {
			sep = i
			break
		}
	}
	if sep < 0 {
		return -1, nil
	}
	return sep, lines[sep:]
}

// TestModelBandSpansFullTerminalWidthTall pins /AC3/AC5 on a tall
// rail-visible terminal: the band now spans the full terminal width (minus the
// 2-col gutter) all the way under the right rail. Previously the band stopped
// at the transcript width, leaving a dead railWidth x bandHeight blank corner to
// its right; now each band row (separator, status strip, composer) reaches the
// full width, and the rail floats above the band without ever overlapping.
func TestModelBandSpansFullTerminalWidthTall(t *testing.T) {
	m := railBandModel(t, 120, 40)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x40")
	}
	// The seam: bandWidth stretches to the full terminal width while
	// transcriptWidth stays rail-shrunk so the history keeps wrapping.
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

	// Every band row reaches the full terminal width: no dead railWidth x
	// bandHeight blank corner to the right of the band.
	for i, r := range band {
		if w := plainWidth(r); w != 120-2 {
			t.Errorf("band row %d (frame row %d) is %d wide, want full terminal width %d (blank corner must be gone)", i, sep+i, w, 120-2)
		}
		// The rail never overlaps the band: its left border never appears on a
		// band row.
		if strings.Contains(r, "│") {
			t.Errorf("band row %d contains the rail's left border; rail must float above the band", sep+i)
		}
	}
}

// TestModelHistoryWrapsAtTranscriptWidthWithRail pins: the
// history pane must keep wrapping to leave room for the rail (unchanged wrap
// width) even though the bottom band now spans full width. renderHistory must
// set its wrap/pane width from transcriptWidth() (rail-shrunk), not from the
// composer/band width. A prompt whose tail lands beyond transcriptWidth but
// within the full band must wrap early enough that its tail stays visible in
// the transcript pane (column < transcriptWidth) instead of being hard-truncated
// behind the rail.
func TestModelHistoryWrapsAtTranscriptWidthWithRail(t *testing.T) {
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
	// The tail must sit on the transcript side of the rail border, never hidden
	// at or past column transcriptWidth.
	for _, ln := range strings.Split(p, "\n") {
		if i := strings.Index(ln, "XYZEND"); i >= 0 && i >= m.tx.transcriptWidth() {
			t.Errorf("history tail column %d not below transcriptWidth=%d (rail-shrunk wrap width)", i, m.tx.transcriptWidth())
		}
	}
}

// TestModelRailEndsOneRowAboveBand pins: the rail is height-bound
// by railClampHeight() = height - bandHeight(), so even when the rail content is
// taller than the room above the band, it clamps to end exactly one row above
// the band's top and never overlaps it. On a short window the rail content
// overflows the available rows, forcing the clamp.
func TestModelRailEndsOneRowAboveBand(t *testing.T) {
	// Short height: the ~14-row rail block dwarfs the ~7 rows the band leaves.
	m := railBandModel(t, 120, 10)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x10")
	}

	plain := plain(view(m))
	sep, _ := bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}

	// The rail's last rendered border row must be exactly one above the band.
	lastRail := -1
	for i, ln := range strings.Split(plain, "\n") {
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

// TestModelRailEndsOneRowAboveBandTall pins at a TALL rail-visible
// terminal: the right rail must extend to exactly one row above the band top at
// every terminal height, never overlapping it. On a tall window the ~14-row
// STATS/CONTEXT/MODEL block is shorter than the room above the band, so styledRail
// must PAD short content up to the clamp height (railClampHeight()) — not just
// trim long content as it did pre-#232 — or the rail stops many rows above the
// band and leaves a dead gap.
func TestModelRailEndsOneRowAboveBandTall(t *testing.T) {
	// Tall height: the ~14-row rail block leaves ~36 rows between the top and the
	// band; without padding the rail would stop ~22 rows short of the band.
	m := railBandModel(t, 120, 40)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible at 120x40")
	}

	plain := plain(view(m))
	sep, _ := bandRowsFrom(plain)
	if sep < 0 {
		t.Fatalf("band separator row not found in frame:\n%q", view(m))
	}

	// The rail's last rendered left-border row must be exactly one above the band.
	lastRail := -1
	for i, ln := range strings.Split(plain, "\n") {
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

// TestModelComposerCaretStaysCorrectWithRail pins: widening the
// band to the full terminal width leaves the composer at column 0 (band
// bottom-pinned), so the hardware caret geometry is unchanged with the rail
// visible at a tall height — the caret stays at the prompt column and follows
// typing as before.
func TestModelComposerCaretStaysCorrectWithRail(t *testing.T) {
	m := railBandModel(t, 120, 40)
	bottom := lineCount(view(m)) - 1

	c := m.View().Cursor
	if c == nil {
		t.Fatal("hardware caret must be attached while the composer is the active surface")
	}
	if c.X != 2 || c.Y != bottom {
		t.Errorf("empty-composer caret with rail = (%d,%d), want (2,%d)", c.X, c.Y, bottom)
	}

	m = typeText(t, m, "hi")
	after := caret(t, m)
	if after.X != 4 || after.Y != bottom {
		t.Errorf("caret after typing %q with rail = (%d,%d), want (4,%d)", "hi", after.X, after.Y, bottom)
	}
}

// TestModelComposerCaretStaysCorrectWithRailWrapped pins for a
// soft-wrapped draft: the composer box grew with bandWidth to the full terminal
// width, so a draft that wraps to multiple composer rows still places the caret
// at the end of the true visible edit row under the rail.
func TestModelComposerCaretStaysCorrectWithRailWrapped(t *testing.T) {
	m := railBandModel(t, 120, 40)
	m = typeText(t, m, strings.Repeat("a", 100)) // wraps at the full-width composer
	if rows := composerRows(m); len(rows) < 2 {
		t.Fatalf("draft must wrap to at least two composer rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "a")
}

// tallBandHeights is the regression-lock size sweep: tall rail-visible
// windows (height > 25 rows) where the rail's ~14-row content is shorter than the
// room above the band, so the pre-#232 layout left a dead railWidth x bandHeight
// blank corner under the rail (the band stopped at the rail-shrunk transcript
// width) and the rail stopped many rows above the band. Keeping width fixed at
// 120 keeps the rail visible at every height, stressing the exact reported
// symptom across the tall range.
var tallBandHeights = []int{26, 30, 35, 40, 50}

// rowRole names a band row by its role for precise, single-row failure messages.
// The band carries exactly three rows: index 0 separator, 1 status strip, then
// composer (possibly wrapped onto extra rows when a draft wraps).
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

// bandRowsForHeight renders a tall rail-visible model at the given height and
// classifies its band rows (separator, status strip, composer) so the
// full-width assertions below fail by the specific row that shortens. It returns
// the band separator frame-row index and the band row strings (separator
// inclusive, rune-counted plain).
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

// TestModelBandSpansFullWidthUnderRailTallSweep pins /AC3 across
// ALL tall rail-visible window sizes, not just the single 120x40 case the #232
// tests use. Pre-#232 the bandWidth seam was rail-shrunk, so at every tall
// height the band stopped at transcriptWidth and left a dead railWidth x
// bandHeight blank corner under the rail. This test sweeps the tall range and
// asserts every band row — separator, status strip, composer — reaches the full
// terminal width (minus the 2-col gutter) all the way under the rail, at every
// height, and that the rail's left border never appears on a band row (no
// overlap).
func TestModelBandSpansFullWidthUnderRailTallSweep(t *testing.T) {
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
				if strings.Contains(r, "│") {
					t.Errorf("h=%d %s row (frame row %d) contains the rail's left border; rail must float above the band, not overlap it", h, rowRole(i), sep+i)
				}
			}
		})
	}
}

// TestModelRailEndsOneRowAboveBandTallSweep pins across ALL tall
// rail-visible window sizes: the rail's content must fill down to exactly one
// row above the band top — never overlapping it and never stopping early above
// it — at every height in the sweep. Pre-#232 styledRail trimmed long content
// but did NOT pad short content, so on tall windows (where the ~14-row rail
// block is shorter than the room above the band) the rail stopped many rows
// above the band and left a dead gap. This sweep asserts the rail's last
// rendered border row == band separator row - 1 at every tall height.
func TestModelRailEndsOneRowAboveBandTallSweep(t *testing.T) {
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
