package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"
)

// fakeSess turns returns a canned answer for tests that drive a Model turn.
func fakeSess(prompt string) Turn {
	return func(ctx context.Context, p string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "hi"}, nil
	}
}

// TestRailRenderStats asserts the rail's STATS section reflects the live
// telemetry: cache hit %, turns, elapsed session time, and token in/out — and
// that no cost line renders (pricing was removed, see issue #374). The rail
// carries the full stats picture permanently.
func TestRailRenderStats(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "STATS") {
		t.Errorf("rail missing STATS section, got: %q", view)
	}
	if !strings.Contains(view, "cache 80%") {
		t.Errorf("rail STATS missing cache gauge, got: %q", view)
	}
	if !strings.Contains(view, "turns 1/250") {
		t.Errorf("rail STATS missing turns, got: %q", view)
	}
	// The cost readout is gone: pricing is provider-specific and churns, so the
	// STATS section shows token and cache figures only, never a derived amount.
	if strings.Contains(view, "cost") {
		t.Errorf("rail STATS must not render a cost line, got: %q", view)
	}
	// The elapsed readout is the session wall-clock: freshly
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
// provider/model/effort/thinking.
func TestRailRenderModel(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), defaultTheme, defaultRailWidth)

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
// rail is STATS / CONTEXT / MODEL only, so detected skills and
// their activation state never appear in the right pane.
func TestRailRenderContext(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme, defaultRailWidth)

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
// hue from the theme palette: the STATS / CONTEXT / MODEL
// headers and their body lines carry the per-section hue's truecolor sequence
// under the default theme, so the three sections read apart at a glance. The
// SKILLS section hue is gone with the section.
func TestRailRenderSectionHues(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme, defaultRailWidth)

	// Default-theme rail hues, as lipgloss truecolor sequences:
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
// only — cache %, turns, token in/out — with no usage-history graph rows
// in any state: the per-turn token/cost sparklines are removed,
// so no unicode-block shape ever appears next to the numbers, even with a
// populated telemetry history.
func TestRailRenderStatsNoGraph(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	// Two turns of unequal usage: the shape the sparkline would have drawn.
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 100, Output: 100})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 300, Output: 300})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme, defaultRailWidth)

	// No graph rows: no usage/cost sparkline labels, no block glyphs.
	if line := lineContaining(view, "usage"); line != "" {
		t.Errorf("STATS must render no usage graph row, got: %q", line)
	}
	if strings.Contains(view, "▁") {
		t.Errorf("STATS must render no sparkline block glyphs, got: %q", view)
	}
	// The numeric readouts stay, fed by the same telemetry (0 hits, 400 in,
	// 400 out). No cost line exists anymore, so only the token/cache figures
	// assert here.
	for _, want := range []string{"cache 0%", "turns 2/250", "400 in", "400 out"} {
		if !strings.Contains(view, want) {
			t.Errorf("STATS missing numeric readout %q, got: %q", want, view)
		}
	}
	if strings.Contains(view, "cost") {
		t.Errorf("STATS must not render a cost line, got: %q", view)
	}
}

// TestModelRailAlwaysOn asserts the rail is the permanent stats surface (issue
// #227): it renders on any window size — wide or narrow, tall or short — and
// ctrl+b no longer toggles it off. The rail only yields on an
// extreme-minimum terminal via the transcript floor (tested separately).
func TestModelRailAlwaysOn(t *testing.T) {
	t.Parallel()
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
			if !mm.tx.railVisible() {
				t.Fatalf("rail must stay visible at %dx%d (permanent surface)", tc.w, tc.h)
			}
			if content := view(mm); !strings.Contains(content, "STATS") {
				t.Errorf("visible rail missing STATS section at %dx%d, got: %q", tc.w, tc.h, content)
			}
		})
	}
}

// TestModelRailTranscriptFloor asserts the transcript keeps a usable hard
// floor on an extreme-minimum terminal: with the rail always
// on, a window too narrow to host a real pane beside it still reserves the
// floor so the transcript stays readable rather than being squeezed away.
func TestModelRailTranscriptFloor(t *testing.T) {
	t.Parallel()
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
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible on an extreme-minimum terminal")
	}
	if tw := m.tx.transcriptWidth(); tw < 20 {
		t.Errorf("transcriptWidth = %d on a 40-col window, want the hard floor >= 20", tw)
	}
}

// TestModelRailNoToggle asserts ctrl+b no longer hides the rail — there is no
// show/hide toggle for the permanent stats surface: pressing it
// leaves the rail visible, and no stray STATS/CONTEXT loss follows.
func TestModelRailNoToggle(t *testing.T) {
	t.Parallel()
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
	if !m.tx.railVisible() {
		t.Fatal("ctrl+b must not hide the rail (no toggle exists)")
	}
	if !strings.Contains(view(m), "STATS") {
		t.Errorf("rail still missing after ctrl+b, got: %q", view(m))
	}
}

// TestModelRailLiveUpdates asserts the visible rail reflects a telemetry
// update drained through the live-delivery path.
func TestModelRailLiveUpdates(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m = asModel(t, nm)

	// The rail is the always-on stats surface; feed a live update.
	te.updates <- TelemetryUpdate{Kind: TelemetryUsage, Hit: 90_000, Miss: 10_000, Output: 5_000}
	cmd := telemetryWait(te)
	msg := cmd()
	nm, _ = m.Update(msg)
	m = asModel(t, nm)

	if !strings.Contains(view(m), "cache 90%") {
		t.Errorf("open rail not live-updating cache gauge, got: %q", view(m))
	}
	// No graph rows in the live view either: the per-turn usage/cost sparklines
	// are removed, so a drained usage update shows in the numeric
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
	t.Parallel()
	m := newTallHistoryModel(t)
	// Wire a rail whose STATS/CONTEXT/MODEL block is taller than the available
	// history viewport in a short window.
	m.deps.Rail = NewRail("opencode-go", "deepseek-v4-flash", "high", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	m.tx.rail = m.deps.Rail
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 12})
	m = asModel(t, nm)

	// The rail is always on, even on a short window.
	if !m.tx.railVisible() {
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
// the separator (and the rail) past the right edge.
func TestModelRailStaysOnScreen(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: fakeSess("hi"),
		Rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a"),
	})
	m = typeText(t, m, "hello there")
	m = submitAndWait(t, m)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(t, nm)
	if !m.tx.railVisible() {
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
	t.Parallel()
	m := NewModel(fakeSess("hi"))
	m = resize(t, m)
	content := view(m)
	if strings.Contains(content, "STATS") {
		t.Errorf("nil rail must render no rail, got: %q", content)
	}
}

// TestModelBandSpansFullWidthWhileTranscriptStaysRailShrunk pins the issue
// #232 seam: the bottom band now sizes itself from its own bandWidth() source
// and spans the full terminal width (minus the 2-col gutter) even when the
// right rail is visible, while transcriptWidth() stays rail-shrunk so the
// history keeps wrapping to leave the rail room. The two widths must diverge on
// a rail-visible window (band wide, transcript narrow).
func TestModelBandSpansFullWidthWhileTranscriptStaysRailShrunk(t *testing.T) {
	t.Parallel()
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
	}{
		{"wide", 120},
		{"narrow", 80},
		// Extreme-minimum rail-visible windows: the separator must inherit
		// transcriptWidth's hard floor (20), not collapse to a
		// sliver.
		{"degenerate", 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nm, _ := m.Update(tea.WindowSizeMsg{Width: tc.w, Height: 30})
			m = asModel(t, nm)
			if !m.tx.railVisible() {
				t.Fatalf("rail must stay visible at %dx30", tc.w)
			}
			if bw, tw := m.tx.bandWidth(), m.tx.transcriptWidth(); bw <= tw {
				t.Errorf("bandWidth = %d must exceed rail-shrunk transcriptWidth = %d; band spans the full terminal width while the history stays rail-shrunk", bw, tw)
			}
			if bw := m.tx.bandWidth(); bw != tc.w-2 {
				t.Errorf("bandWidth = %d, want full terminal width minus gutter = %d", bw, tc.w-2)
			}
			if m.tx.bandWidth() < 2 {
				t.Errorf("bandWidth %d must be >= 2 so the accent separator still reads as a line", m.tx.bandWidth())
			}
		})
	}
}

// TestModelBandWidthRailHiddenTiny pins the byte-identical seam on a rail-hidden
// tiny window too: with no right rail, bandWidth
// and transcriptWidth follow the same formula and must still agree, even on a
// sliver where renderBand's own clamp keeps the separator readable.
func TestModelBandWidthRailHiddenTiny(t *testing.T) {
	t.Parallel()
	m := NewModel(fakeSess("hi")) // no rail wired -> railVisible() == false
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 3, Height: 8})
	m = asModel(t, nm)
	if m.tx.railVisible() {
		t.Fatal("model without a wired rail must not show the rail")
	}
	if bw, tw := m.tx.bandWidth(), m.tx.transcriptWidth(); bw != tw {
		t.Errorf("rail-hidden tiny window: bandWidth = %d, transcriptWidth = %d; seam must be byte-identical", bw, tw)
	}
}

// TestModelBandWidthIndependentOfComposer pins the decoupling of band width: the
// band width must be derived from its own source and never read the composer's
// width. Flipping the composer width (via SetWidth) must not move bandWidth(),
// and transcriptWidth() must likewise not read composer width once the terminal
// width is known.
func TestModelBandWidthIndependentOfComposer(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = asModel(t, nm)

	// Changing the composer's width drives the composer's own wrap, but must not
	// influence the band: bandWidth() has its own terminal-width source.
	before := m.tx.bandWidth()
	m.composer.SetWidth(200)
	after := m.tx.bandWidth()
	if before != after {
		t.Errorf("bandWidth changed %d -> %d after composer width changed; band must be independent of composer width", before, after)
	}

	// transcriptWidth() must be derived solely from the terminal width and the
	// rail, never the composer width, once a resize has landed.
	twA := m.tx.transcriptWidth()
	m.composer.SetWidth(5)
	twB := m.tx.transcriptWidth()
	if twA != twB {
		t.Errorf("transcriptWidth changed %d -> %d after composer width changed; must not read composer width", twA, twB)
	}
}

// TestRailRenderCtxLine asserts the STATS `ctx` line renders after the `tokens`
// line with the latest per-turn live context-window size, human-readable via
// formatTokens, in the normal (stats-hue) styling below the warning threshold
//.
func TestRailRenderCtxLine(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 137_000})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	// 137k live ctx -> "137.0k" (formatTokens), rendered after the tokens line.
	if !strings.Contains(view, "ctx 137.0k") {
		t.Errorf("rail STATS missing ctx line, got: %q", view)
	}
	// The ctx line follows the tokens line in the STATS body.
	if !strings.Contains(view, "tokens") || strings.Index(view, "ctx 137.0k") < strings.Index(view, "tokens") {
		t.Errorf("ctx line must render after the tokens line, got: %q", view)
	}
	// Normal styling: the ctx line carries the stats hue, not the warning hue.
	if line := lineContaining(view, "ctx 137.0k"); !strings.Contains(line, "\x1b[38;2;224;175;104m") {
		t.Errorf("ctx line below threshold = %q, want the stats hue (not warning)", line)
	}
}

// TestRailRenderCtxWarnAboveThreshold asserts the STATS `ctx` line renders in
// warning styling once the live context reaches the 150k threshold, while the
// readout still shows the human-readable size.
func TestRailRenderCtxWarnAboveThreshold(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 160_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "ctx 160.0k") {
		t.Errorf("rail STATS missing ctx value at threshold, got: %q", view)
	}
	if ctx := lineContaining(view, "ctx 160.0k"); !strings.Contains(ctx, "\x1b[38;2;247;118;142m") {
		t.Errorf("ctx line at threshold = %q, want warning hue (default error #F7768E)", ctx)
	}
}

// TestRailRenderCtxPostCompactionRollback asserts the ctx readout and its
// warning clear on the next turn once a compaction shrinks the real context:
// after an over-threshold turn, the following turn's smaller live ctx renders
// normally again, proving the readout is live-per-turn, not cumulative
//.
func TestRailRenderCtxPostCompactionRollback(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 160_000})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 48_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	// The readout reflects the smaller post-compaction size, human-readable.
	if !strings.Contains(view, "ctx 48.0k") {
		t.Errorf("rail STATS ctx did not roll back to 48.0k after compaction, got: %q", view)
	}
	// The warning styling is gone with the old, over-threshold size.
	if line := lineContaining(view, "ctx 48.0k"); strings.Contains(line, "\x1b[38;2;247;118;142m") {
		t.Errorf("ctx line after compaction %q must not carry the warning hue", line)
	}
}

// TestRailRenderStatsWide asserts the rail renders aligned key-value columns at
// wider-than-default widths: the key column is wider and values are right-padded
// for alignment, making the rail read as a real stat ledger.
func TestRailRenderStatsWide(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, 50)

	// At width 50 the key column is wide enough for right-aligned values.
	if !strings.Contains(view, "cache") {
		t.Errorf("rail STATS missing cache at wide width, got: %q", view)
	}
	if !strings.Contains(view, "turns") {
		t.Errorf("rail STATS missing turns at wide width, got: %q", view)
	}
	// The values should still contain expected data.
	if !strings.Contains(view, "80%") {
		t.Errorf("rail STATS missing 80%% at wide width, got: %q", view)
	}
	if !strings.Contains(view, "1/250") {
		t.Errorf("rail STATS missing 1/250 at wide width, got: %q", view)
	}
}

// TestRailRenderModelWide asserts the MODEL section renders the full
// provider/model name without truncation at wider widths.
func TestRailRenderModelWide(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), defaultTheme, 50)

	// At width 50, the full provider/model should fit without truncation.
	if !strings.Contains(view, "opencode-go/deepseek-v4-flash") {
		t.Errorf("rail MODEL truncated at wide width 50, got: %q", view)
	}
	if strings.Contains(view, "opencode-go/deepseek-v4-f…") {
		t.Errorf("rail MODEL should not truncate at wide width 50, got: %q", view)
	}
}

// TestRailRenderStatsNarrow asserts the rail degrades gracefully at narrow
// widths without wrapping or overlapping.
func TestRailRenderStatsNarrow(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, 24)

	// At width 24 the content degrades but sections still exist.
	if !strings.Contains(view, "STATS") {
		t.Errorf("rail missing STATS at narrow width, got: %q", view)
	}
	// No line should exceed the rail width.
	for _, ln := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
		stripped := plain(ln)
		if w := lipgloss.Width(stripped); w > 24 {
			t.Errorf("narrow rail line %d columns wide, max %d: %q", w, 24, stripped)
		}
	}
}

// TestRailDefaultWidthUnchanged asserts the default-width rendering is
// unchanged from today.
func TestRailDefaultWidthUnchanged(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	// Default width: same truncation as before (provider/model truncated).
	if !strings.Contains(view, "opencode-go/deepseek-v4-f…") {
		t.Errorf("rail MODEL should truncate at default width, got: %q", view)
	}
	if !strings.Contains(view, "cache 80%") {
		t.Errorf("rail STATS missing cache at default width, got: %q", view)
	}
	if !strings.Contains(view, "turns 1/250") {
		t.Errorf("rail STATS missing turns at default width, got: %q", view)
	}
}

// TestRailWideAlignment asserts that at a wide rail width the key-value pairs
// are column-aligned: keys are padded to a consistent width so values start at
// the same column.
func TestRailWideAlignment(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.renderStats(te, defaultTheme, 50)

	// At width 50, the "cache" and "turns" lines should have values starting
	// at the same column — the key column is consistently padded.
	cacheLine := lineContaining(view, "80%")
	turnsLine := lineContaining(view, "1/250")
	if cacheLine == "" || turnsLine == "" {
		t.Fatalf("missing cache or turns line in wide stats: %q", view)
	}
	// Strip ANSI to get plain text, then find value start position.
	cachePlain := plain(cacheLine)
	turnsPlain := plain(turnsLine)
	// Both values should start after the padded key column — the same column
	// position in the output.
	cacheValIdx := strings.Index(cachePlain, "80%")
	turnsValIdx := strings.Index(turnsPlain, "1/250")
	if cacheValIdx != turnsValIdx {
		t.Errorf("values not aligned at wide width: cache value starts at %d, turns value starts at %d\n  cache: %q\n  turns: %q", cacheValIdx, turnsValIdx, cachePlain, turnsPlain)
	}
}

// TestRailRenderWideNoOverflow asserts no rail line exceeds the rail width at
// wide widths (no content overflow or wrap).
func TestRailRenderWideNoOverflow(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	for _, w := range []int{40, 50, 60, 80} {
		view := r.render(te, defaultTheme, w)
		for _, ln := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
			stripped := plain(ln)
			if lw := lipgloss.Width(stripped); lw > w {
				t.Errorf("wide rail line %d columns wide at width %d, max %d: %q", lw, w, w, stripped)
			}
		}
	}
}

// TestRailWideValuesFuller asserts that values at wider widths show more content
// than at default width — the rail actually pays off the extra columns
//.
func TestRailWideValuesFuller(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")

	// Default width: provider/model truncated.
	defView := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme, defaultRailWidth)
	// Wide width: provider/model should NOT be truncated.
	wideView := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme, 55)

	// At default, provider/model is truncated with ellipsis.
	if !strings.Contains(defView, "opencode-go/deepseek-v4-f…") {
		t.Errorf("default width should truncate provider/model, got: %q", defView)
	}
	// At wide, provider/model fits fully.
	if !strings.Contains(wideView, "opencode-go/deepseek-v4-flash") {
		t.Errorf("wide width should show full provider/model, got: %q", wideView)
	}
	// The tokens line at wide width should show more than at default.
	defTokens := lineContaining(defView, "tokens")
	wideTokens := lineContaining(wideView, "tokens")
	if defTokens == "" || wideTokens == "" {
		t.Fatalf("missing tokens line: default=%q wide=%q", defTokens, wideTokens)
	}
	defPlain := plain(defTokens)
	widePlain := plain(wideTokens)
	if len(widePlain) < len(defPlain) {
		t.Errorf("wide tokens line shorter than default: %d vs %d\n  default: %q\n  wide:    %q", len(widePlain), len(defPlain), defPlain, widePlain)
	}
}
