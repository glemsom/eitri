package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// railBandModel builds a sized chat model wired with both a telemetry strip
// (so the band carries a status row) and the right context rail, at the given
// terminal dimensions. It is the fixture for the issue #232 edge-to-edge bottom
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

// TestModelBandSpansFullTerminalWidthTall pins issue #232 AC1/AC3/AC5 on a tall
// rail-visible terminal: the band now spans the full terminal width (minus the
// 2-col gutter) all the way under the right rail. Previously the band stopped
// at the transcript width, leaving a dead railWidth x bandHeight blank corner to
// its right; now each band row (separator, status strip, composer) reaches the
// full width, and the rail floats above the band without ever overlapping.
func TestModelBandSpansFullTerminalWidthTall(t *testing.T) {
	m := railBandModel(t, 120, 40)
	if !m.railVisible() {
		t.Fatal("rail must stay visible at 120x40")
	}
	// The seam: bandWidth stretches to the full terminal width while
	// transcriptWidth stays rail-shrunk so the history keeps wrapping.
	if bw, tw := m.bandWidth(), m.transcriptWidth(); bw <= tw {
		t.Errorf("bandWidth = %d must exceed rail-shrunk transcriptWidth = %d (band spans full terminal width, history stays rail-shrunk)", bw, tw)
	}
	if w := m.bandWidth(); w != 120-2 {
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

// TestModelRailEndsOneRowAboveBand pins issue #232 AC4: the rail is height-bound
// by railClampHeight() = height - bandHeight(), so even when the rail content is
// taller than the room above the band, it clamps to end exactly one row above
// the band's top and never overlaps it. On a short window the rail content
// overflows the available rows, forcing the clamp.
func TestModelRailEndsOneRowAboveBand(t *testing.T) {
	// Short height: the ~14-row rail block dwarfs the ~7 rows the band leaves.
	m := railBandModel(t, 120, 10)
	if !m.railVisible() {
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

// TestModelComposerCaretStaysCorrectWithRail pins issue #232 AC6: widening the
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
		t.Errorf("caret after typing %q with rail = (%d,%d), want (4,%d)", "hi", c.X, c.Y, bottom)
	}
}

// TestModelComposerCaretStaysCorrectWithRailWrapped pins issue #232 AC6 for a
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
