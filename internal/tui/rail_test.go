package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeSess turns returns a canned answer for tests that drive a Model turn.
func fakeSess(prompt string) Turn {
	return func(ctx context.Context, p string) (TurnResult, error) {
		return TurnResult{Answer: "hi"}, nil
	}
}

// TestRailRenderStats asserts the rail's STATS section reflects the live
// telemetry: cache hit %, cost, turns, elapsed session time, and token in/out
// (issue #88; issue #227 added the elapsed readout so the rail carries the
// full stats picture permanently).
func TestRailRenderStats(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme)

	if !strings.Contains(view, "STATS") {
		t.Errorf("rail missing STATS section, got: %q", view)
	}
	if !strings.Contains(view, "cache 80%") {
		t.Errorf("rail STATS missing cache gauge, got: %q", view)
	}
	if !strings.Contains(view, "turns 1/250") {
		t.Errorf("rail STATS missing turns, got: %q", view)
	}
	if !strings.Contains(view, "cost $0.00658") {
		t.Errorf("rail STATS missing cost, got: %q", view)
	}
	// The elapsed readout is the session wall-clock (issue #227): freshly
	// constructed, the session has run for just enough time to show "0s".
	if !strings.Contains(view, "elapsed 0s") {
		t.Errorf("rail STATS missing elapsed readout, got: %q", view)
	}
	// 100k hit + 25k miss = 125k in; 10k out.
	if !strings.Contains(view, "125.0k in") || !strings.Contains(view, "10.0k out") {
		t.Errorf("rail STATS missing token in/out, got: %q", view)
	}
}

// TestRailRenderModel asserts the MODEL section reflects the session's static
// provider/model/effort/thinking (issue #88).
func TestRailRenderModel(t *testing.T) {
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), defaultTheme)

	if !strings.Contains(view, "MODEL") {
		t.Errorf("rail missing MODEL section, got: %q", view)
	}
	if !strings.Contains(view, "opencode-go/deepseek-v4-f…") {
		t.Errorf("rail MODEL missing provider/model (truncated to the rail width), got: %q", view)
	}
	if !strings.Contains(view, "effort high") {
		t.Errorf("rail MODEL missing effort, got: %q", view)
	}
	if !strings.Contains(view, "thinking off") {
		t.Errorf("rail MODEL missing thinking flag, got: %q", view)
	}
}

// TestRailRenderContext asserts the CONTEXT section reflects the session id
// and session temp path, and that the rail renders no SKILLS section — the
// rail is STATS / CONTEXT / MODEL only (issue #188), so detected skills and
// their activation state never appear in the right pane.
func TestRailRenderContext(t *testing.T) {
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme)

	if !strings.Contains(view, "CONTEXT") {
		t.Errorf("rail missing CONTEXT section, got: %q", view)
	}
	if strings.Contains(view, "SKILLS") {
		t.Errorf("rail must not render a SKILLS section, got: %q", view)
	}
	if !strings.Contains(view, "session eitri-9f2c") {
		t.Errorf("rail CONTEXT missing session id, got: %q", view)
	}
	if !strings.Contains(view, "temp /tmp/eitri-9f2c") {
		t.Errorf("rail CONTEXT missing session temp path, got: %q", view)
	}
}

// TestRailRenderSectionHues asserts each rail section renders with a distinct
// hue from the theme palette (issue #182 AC1): the STATS / CONTEXT / MODEL
// headers and their body lines carry the per-section hue's truecolor sequence
// under the default theme, so the three sections read apart at a glance. The
// SKILLS section hue is gone with the section (issue #188).
func TestRailRenderSectionHues(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme)

	// Default-theme rail hues, as lipgloss truecolor sequences (issue #178:
	// every palette entry is a hex value; the output layer downsamples for
	// non-truecolor terminals).
	cases := []struct {
		section string
		hue     string
	}{
		{"STATS", "\x1b[1;38;2;224;175;104m"},   // stats amber #E0AF68
		{"CONTEXT", "\x1b[1;38;2;125;207;255m"}, // context light-blue #7DCFFF
		{"MODEL", "\x1b[1;38;2;158;206;106m"},   // model green #9ECE6A
	}
	for _, tc := range cases {
		hdr := lineContaining(view, tc.section)
		if hdr == "" {
			t.Fatalf("rail missing %s section, got: %q", tc.section, view)
		}
		if !strings.Contains(hdr, tc.hue) {
			t.Errorf("%s header = %q, want hue %q", tc.section, hdr, tc.hue)
		}
	}
	// Body lines pick up the section hue too — a STATS value carries the stats
	// amber, not a different section's hue.
	if cache := lineContaining(view, "cache 80%"); !strings.Contains(cache, "\x1b[38;2;224;175;104m") {
		t.Errorf("STATS body line = %q, want the stats hue", cache)
	}
	if model := lineContaining(view, "opencode-go/deepseek-v4-f…"); !strings.Contains(model, "\x1b[38;2;158;206;106m") {
		t.Errorf("MODEL body line = %q, want the model hue", model)
	}
}

// TestRailRenderStatsNoGraph asserts the STATS section renders numeric lines
// only — cache %, cost, turns, token in/out — with no usage-history graph rows
// in any state (issue #189): the per-turn token/cost sparklines are removed,
// so no unicode-block shape ever appears next to the numbers, even with a
// populated telemetry history.
func TestRailRenderStatsNoGraph(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	// Two turns of unequal usage: the shape the sparkline would have drawn.
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 100, Output: 100})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 300, Output: 300})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme)

	// No graph rows: no usage/cost sparkline labels, no block glyphs.
	if line := lineContaining(view, "usage"); line != "" {
		t.Errorf("STATS must render no usage graph row, got: %q", line)
	}
	if strings.Contains(view, "▁") {
		t.Errorf("STATS must render no sparkline block glyphs, got: %q", view)
	}
	// The numeric readouts stay, fed by the same telemetry (0 hits, 400 in,
	// 400 out @ $0.14/$0.28 per 1M = $0.000168).
	for _, want := range []string{"cache 0%", "cost $0.000168", "turns 2/250", "400 in", "400 out"} {
		if !strings.Contains(view, want) {
			t.Errorf("STATS missing numeric readout %q, got: %q", want, view)
		}
	}
}

// TestModelRailAlwaysOn asserts the rail is the permanent stats surface (issue
// #227): it renders on any window size — wide or narrow, tall or short — and
// ctrl+b no longer toggles it off. The rail only yields on an
// extreme-minimum terminal via the transcript floor (tested separately).
func TestModelRailAlwaysOn(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})

	for _, tc := range []struct {
		name string
		w    int
		h    int
	}{
		{"wide", 140, 24},
		{"narrow", 80, 24},
		{"short", 140, 10},
		{"tiny", 60, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nm, _ := m.Update(tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
			mm := asModel(t, nm)
			if !mm.railVisible() {
				t.Fatalf("rail must stay visible at %dx%d (permanent surface)", tc.w, tc.h)
			}
			if content := view(mm); !strings.Contains(content, "STATS") {
				t.Errorf("visible rail missing STATS section at %dx%d, got: %q", tc.w, tc.h, content)
			}
		})
	}
}

// TestModelRailTranscriptFloor asserts the transcript keeps a usable hard
// floor on an extreme-minimum terminal (issue #227 AC3): with the rail always
// on, a window too narrow to host a real pane beside it still reserves the
// floor so the transcript stays readable rather than being squeezed away.
func TestModelRailTranscriptFloor(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})

	// An extreme-minimum terminal (narrower than railWidth + floor + gutter):
	// the rail is visible, but the transcript columns are floored, never 0.
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("rail must stay visible on an extreme-minimum terminal")
	}
	if tw := m.transcriptWidth(); tw < 20 {
		t.Errorf("transcriptWidth = %d on a 40-col window, want the hard floor >= 20", tw)
	}
}

// TestModelRailNoToggle asserts ctrl+b no longer hides the rail (issue #227 AC2
// — there is no show/hide toggle for the permanent stats surface): pressing it
// leaves the rail visible, and no stray STATS/CONTEXT loss follows.
func TestModelRailNoToggle(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = asModel(t, nm)

	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("ctrl+b must not hide the rail (no toggle exists)")
	}
	if !strings.Contains(view(m), "STATS") {
		t.Errorf("rail still missing after ctrl+b, got: %q", view(m))
	}
}

// TestModelRailLiveUpdates asserts the visible rail reflects a telemetry
// update drained through the live-delivery path (issue #88 AC4).
func TestModelRailLiveUpdates(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m = asModel(t, nm)

	// The rail is the always-on stats surface (issue #227); feed a live update.
	te.updates <- TelemetryUpdate{Kind: TelemetryUsage, Hit: 90_000, Miss: 10_000, Output: 5_000}
	cmd := telemetryWait(te)
	msg := cmd()
	nm, _ = m.Update(msg)
	m = asModel(t, nm)

	if !strings.Contains(view(m), "cache 90%") {
		t.Errorf("open rail not live-updating cache gauge, got: %q", view(m))
	}
	// No graph rows in the live view either: the per-turn usage/cost sparklines
	// are removed (issue #189), so a drained usage update shows in the numeric
	// readouts only.
	if strings.Contains(view(m), "usage") || strings.Contains(view(m), "▁") {
		t.Errorf("open rail must not render graph rows, got: %q", view(m))
	}
}

// TestModelRailHeightMatchesHistory asserts the right rail honours the same
// visible height as the history region (issue T05 AC1): in a sized window where
// the rail content alone would overflow, the rail
// clips to the same row height as the history viewport so the two panes form
// one coherent row and the whole view stays within the terminal height.
func TestModelRailHeightMatchesHistory(t *testing.T) {
	m := newTallHistoryModel(t)
	// Wire a rail whose STATS/CONTEXT/MODEL block is taller than the available
	// history viewport in a short window.
	m.deps.Rail = NewRail("opencode-go", "deepseek-v4-flash", "high", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	m.rail = m.deps.Rail
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 12})
	m = asModel(t, nm)

	// The rail is always on (issue #227), even on a short window.
	if !m.railVisible() {
		t.Fatal("rail must stay visible on a short window")
	}
	content := view(m)

	// The rail is clipped to the same height as the history viewport, so even
	// though the raw rail block is ~16 lines the whole joined row stays within
	// the terminal height. Before the clip the rail overflows independently
	// while the history clips.
	if n := len(strings.Split(strings.TrimRight(content, "\n"), "\n")); n > 12 {
		t.Errorf("view (%d lines) exceeds terminal height 12 with the rail open, got:\n%q", n, content)
	}
}

// TestModelRailStaysOnScreen asserts the joined transcript+rail row never runs
// off the terminal's right edge: every rendered row fits the window width, the
// band separator starts its own row (column 0), and the rail's left border is
// visible on screen. Regression for the history viewport region rendering
// newline-joined rows with no trailing newline, which fused the band separator
// onto the viewport's last padded row — doubling that row's width and shoving
// the separator (and the rail) past the right edge (issue #88, T1 pivot).
func TestModelRailStaysOnScreen(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: fakeSess("hi"),
		Rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a"),
	})
	m = typeText(t, m, "hello there")
	m = submitAndWait(t, m)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("rail should stay visible at 120x30 (permanent surface)")
	}

	content := plain(view(m))
	for i, ln := range strings.Split(content, "\n") {
		if w := len([]rune(ln)); w > 120 {
			t.Errorf("row %d is %d columns wide (terminal 120): %q", i, w, ln)
		}
	}
	// The band separator must begin at column 0 on its own row, not glued onto
	// the tail of the scroll region's last (padded) row.
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "─") {
			return // separator on its own row, rail intact
		}
	}
	t.Errorf("band separator is not on its own row; rail+separator overflow right edge:\n%q", content)
}

// TestModelRailNoPanicWithoutFeed asserts the model renders fine with a nil
// rail and no telemetry (the plain chat default), so wiring is optional.
func TestModelRailNoPanicWithoutFeed(t *testing.T) {
	m := NewModel(fakeSess("hi"))
	m = resize(t, m)
	content := view(m)
	if strings.Contains(content, "STATS") {
		t.Errorf("nil rail must render no rail, got: %q", content)
	}
}
