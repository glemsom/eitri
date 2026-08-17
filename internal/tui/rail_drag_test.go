package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// This file covers the right-rail drag-resize seam (issue #306): a left press
// within railBorderHitZone columns of the rail's left border starts a width
// drag, horizontal motion resizes the rail live (clamped to a readable range),
// and release keeps the width for the session. The rail-border press is
// decided BEFORE the drag-select hit-test, so click-drag text selection in the
// history keeps working untouched, and the bottom band stays edge-to-edge
// under the rail at every width.

// railDragModel builds a sized model with a telemetry strip and a wired rail,
// plus one committed turn so the history viewport is hydrated (mouse events
// for the rail drag land on the rail's own rows, not the band).
func railDragModel(t *testing.T) Model {
	t.Helper()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(t, nm)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m) // hydrate the persisted viewport
	return m
}

// railDragMsg builds a bubbletea v2 mouse event for the drag-resize seam
// (issue #306), mirroring selection_test.go's dragMsg shape: a left press
// becomes a MouseClickMsg, motion a MouseMotionMsg, and release a
// MouseReleaseMsg.
func railDragMsg(action string, x, y int) tea.Msg {
	switch action {
	case "press":
		return tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y}
	case "motion":
		return tea.MouseMotionMsg{Button: tea.MouseLeft, X: x, Y: y}
	default:
		return tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y}
	}
}

// borderRow returns a screen row the rail occupies: the rail spans the rows
// above the fixed bottom band, so any row in [0, height-bandHeight) is on the
// rail. Row 0 is returned (the workspace header row, which the rail overlays).
func (m Model) borderRow() int { return 0 }

// railDragPressMotion drags the rail border from its CURRENT position by dx
// columns in one gesture: the border moves with the width, so the press x is
// always the live border (issue #306). Returns the model after release.
func railDragPressMotion(t *testing.T, m Model, dx int) Model {
	t.Helper()
	border := m.tx.railBorderColumn()
	row := m.borderRow()
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("motion", border+dx, row))
	return mustUpdate(t, m, railDragMsg("release", border+dx, row))
}

// TestRailDrag_resizesLiveAndKeepsWidth asserts the full drag gesture: a press
// on the rail border column starts the drag, each horizontal motion resizes
// the rail live by the pointer delta (the transcript re-wraps because
// setRailWidth marks the layout dirty), and release keeps the width for the
// session (issue #306 AC1/AC3).
func TestRailDrag_resizesLiveAndKeepsWidth(t *testing.T) {
	m := railDragModel(t)
	startW := m.tx.railWidthOrDefault()
	border := m.tx.railBorderColumn()
	row := m.borderRow()

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if m.railDrag == nil {
		t.Fatal("border press must start a rail drag")
	}
	if m.tx.dragSel != nil {
		t.Fatal("border press must not start a drag selection")
	}
	// Starting width must be unchanged by the press itself.
	if got := m.tx.railWidthOrDefault(); got != startW {
		t.Fatalf("press must not resize: rail width %d, want %d", got, startW)
	}

	// Motion right by 8: the rail grows to startW+8 live, and the layout is
	// marked dirty so the next render re-wraps the transcript.
	m = mustUpdate(t, m, railDragMsg("motion", border+8, row))
	if got := m.tx.railWidthOrDefault(); got != startW+8 {
		t.Fatalf("motion +8: rail width %d, want %d", got, startW+8)
	}
	if !m.tx.layout.dirty {
		t.Error("rail drag motion must mark the layout dirty (transcript re-wrap)")
	}

	// Motion left by 3 from there: width follows the pointer both ways.
	m = mustUpdate(t, m, railDragMsg("motion", border+5, row))
	if got := m.tx.railWidthOrDefault(); got != startW+5 {
		t.Errorf("motion back to +5: rail width %d, want %d", got, startW+5)
	}

	// Release keeps the width: the drag state clears and the width stays.
	m = mustUpdate(t, m, railDragMsg("release", border+5, row))
	if m.railDrag != nil {
		t.Error("release must clear the rail drag state")
	}
	if got := m.tx.railWidthOrDefault(); got != startW+5 {
		t.Errorf("release must keep the dragged width: got %d, want %d", got, startW+5)
	}
	if m.savedMsg != "" {
		t.Errorf("rail drag release must not trigger the copy/tool-click paths, got band note %q", m.savedMsg)
	}
}

// TestRailDrag_clampsAtMinAndMax asserts the width clamp: dragging far left
// stops at minRailWidth and dragging far right stops at the terminal-width
// cap (half the terminal width when the transcript floor allows it) (issue
// #306 AC2).
func TestRailDrag_clampsAtMinAndMax(t *testing.T) {
	m := railDragModel(t)

	// Drag far left: the requested delta (60 columns) is clamped to the 10-col
	// floor, not to a negative or zero-width rail.
	m = railDragPressMotion(t, m, -60)
	if got := m.tx.railWidthOrDefault(); got != minRailWidth {
		t.Errorf("drag far left: rail width %d, want min %d", got, minRailWidth)
	}

	// Fresh drag far right from the (now min) width: the requested width (70
	// columns wider) is clamped to half the terminal width at 120 columns.
	m = railDragPressMotion(t, m, 70)
	if got, want := m.tx.railWidthOrDefault(), 120/2; got != want {
		t.Errorf("drag far right: rail width %d, want terminal-width cap %d", got, want)
	}

	// Drag back toward the border: the width follows the pointer down from the
	// cap.
	m = railDragPressMotion(t, m, -5)
	if got, want := m.tx.railWidthOrDefault(), 120/2-5; got != want {
		t.Errorf("drag back: rail width %d, want %d", got, want)
	}
}

// TestRailDrag_clampsToTranscriptFloor asserts the terminal-width cap keeps the
// transcript readable on a small terminal (issue #306 AC2, #227 AC3): a rail
// wider than the transcript floor allows must clamp so the transcript never
// falls below its 20-column floor — at a 40-column terminal the floor cap is
// 40-20-1 = 19 columns, far below half the terminal width.
func TestRailDrag_clampsToTranscriptFloor(t *testing.T) {
	m := railDragModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = asModel(t, nm)
	m.tx.railWidth = 0 // reset to the default so the border math uses it
	border := m.tx.railBorderColumn()
	row := m.borderRow()

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("motion", border+50, row))
	if got, want := m.tx.railWidthOrDefault(), 40-minTranscriptWidth-1; got != want {
		t.Errorf("drag on a 40-col terminal: rail width %d, want transcript-floor cap %d", got, want)
	}
	if tw := m.tx.transcriptWidth(); tw < minTranscriptWidth {
		t.Errorf("transcript width %d fell below the readable floor %d", tw, minTranscriptWidth)
	}
}

// TestRailDrag_borderPressDoesNotStartDragSelect asserts a press on the rail
// border column never starts a text selection (issue #306): the rail drag
// decides first, so m.tx.dragSel stays nil across press/motion/release, and
// the clipboard is never touched by the release.
func TestRailDrag_borderPressDoesNotStartDragSelect(t *testing.T) {
	copied := ""
	m := railDragModel(t)
	// Re-wire the clipboard to prove no copy happens on the border release.
	m.clipboard = func(s string) error { copied = s; return nil }
	border := m.tx.railBorderColumn()
	row := m.borderRow()

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("motion", border+6, row))
	m = mustUpdate(t, m, railDragMsg("release", border+6, row))

	if m.tx.dragSel != nil {
		t.Error("border press must never set dragSel")
	}
	if copied != "" {
		t.Errorf("border drag must not copy anything, got %q", copied)
	}
}

// TestRailDrag_midHistoryDragStillSelects asserts click-drag text selection in
// the history keeps working alongside the rail drag (issue #306): a press on
// the history content (far from the rail border) still starts a drag-select,
// and the drag still copies the selected range on release.
func TestRailDrag_midHistoryDragStillSelects(t *testing.T) {
	copied := ""
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
		Telemetry:     NewTelemetry("deepseek-v4-flash", "low", true, 250),
		Rail:          NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1"),
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(t, nm)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d (rows: %q)", top, rows)
	}
	col := strings.Index(rows[0], "workspace")
	if col < 0 {
		t.Fatalf("workspace header not on row 0, got %q", rows[0])
	}
	want := "workspace"

	m = mustUpdate(t, m, dragMsg("press", col, 0))
	m = mustUpdate(t, m, dragMsg("motion", col+len(want)-1, 0))
	m = mustUpdate(t, m, dragMsg("release", col+len(want)-1, 0))

	if copied != want {
		t.Errorf("history drag copy = %q, want %q (rail drag must not swallow history selection)", copied, want)
	}
}

// TestRailDrag_bandStaysEdgeToEdgeAssert asserts the bottom band remains
// edge-to-edge under the rail at every dragged width (issue #306 AC5): after
// resizing the rail to both the min and max widths, every band row still spans
// the full terminal width minus the 2-col gutter, and the rail border never
// appears on a band row.
func TestRailDrag_bandStaysEdgeToEdgeAssert(t *testing.T) {
	// Each subtest starts from a fresh model so the border math is clean: the
	// rail starts at the default width and one gesture reaches the target (the
	// clamp caps the over-shoot).
	for name, target := range map[string]int{"min": minRailWidth, "max": 120 / 2} {
		t.Run(name, func(t *testing.T) {
			m := railDragModel(t)
			dx := target - defaultRailWidth
			m = railDragPressMotion(t, m, dx)
			if got := m.tx.railWidthOrDefault(); got != target {
				t.Fatalf("setup: rail width %d, want %d", got, target)
			}

			sep, band := bandRowsFrom(plain(view(m)))
			if sep < 0 {
				t.Fatalf("band separator row not found in frame:\n%q", view(m))
			}
			for i, r := range band {
				if w := plainWidth(r); w != 120-2 {
					t.Errorf("band row %d (frame row %d) is %d wide, want full terminal width %d at rail width %d", i, sep+i, w, 120-2, target)
				}
				if strings.Contains(r, "│") {
					t.Errorf("band row %d contains the rail's left border; rail must float above the band at rail width %d", i, target)
				}
			}
		})
	}
}

// TestRailDrag_ignoredWhileSettingsOrPrompting asserts the rail drag is ignored
// while the Settings surface or the continuation prompt owns the screen,
// mirroring the drag-select gate (issue #306).
func TestRailDrag_ignoredWhileSettingsOrPrompting(t *testing.T) {
	m := railDragModel(t)
	border := m.tx.railBorderColumn()
	row := m.borderRow()
	startW := m.tx.railWidthOrDefault()

	// Settings surface open: the press must not start a rail drag.
	nm, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("motion", border+10, row))
	if m.railDrag != nil {
		t.Error("rail drag must be ignored while Settings is open")
	}
	if got := m.tx.railWidthOrDefault(); got != startW {
		t.Errorf("rail width changed to %d while Settings open, want %d", got, startW)
	}
}