package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func fakeSess(prompt string) func(context.Context, string, string) (TurnResult, error) {
	return func(ctx context.Context, p string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "hi"}, nil
	}
}

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
	if strings.Contains(view, "cost") {
		t.Errorf("rail STATS must not render a cost line, got: %q", view)
	}
	if !strings.Contains(view, "elapsed 0s") {
		t.Errorf("rail STATS missing elapsed readout, got: %q", view)
	}
	if !strings.Contains(view, "125.0k in") || !strings.Contains(view, "10.0k out") {
		t.Errorf("rail STATS missing token in/out, got: %q", view)
	}
}

func TestRailRenderModel(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "MODEL") {
		t.Errorf("rail missing MODEL section, got: %q", view)
	}
	if !strings.Contains(view, "provider opencode-go") {
		t.Errorf("rail MODEL missing separated provider, got: %q", view)
	}
	if !strings.Contains(view, "model deepseek-v4-flash") {
		t.Errorf("rail MODEL missing separated model, got: %q", view)
	}
	if !strings.Contains(view, "mode effort:high think:off") {
		t.Errorf("rail MODEL missing compact mode metadata, got: %q", view)
	}
}

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

func TestRailRenderSectionHues(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme, defaultRailWidth)

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
	if cache := lineContaining(view, "cache 80%"); !strings.Contains(cache, "\x1b[38;2;224;175;104m") {
		t.Errorf("STATS body line = %q, want the stats hue", cache)
	}
	if model := lineContaining(view, "model deepseek-v4-flash"); !strings.Contains(model, "\x1b[38;2;158;206;106m") {
		t.Errorf("MODEL body line = %q, want the model hue", model)
	}
}

func TestRailRenderStatsNoGraph(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 100, Output: 100})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 300, Output: 300})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if line := lineContaining(view, "usage"); line != "" {
		t.Errorf("STATS must render no usage graph row, got: %q", line)
	}
	if strings.Contains(view, "▁") {
		t.Errorf("STATS must render no sparkline block glyphs, got: %q", view)
	}
	for _, want := range []string{"cache 0%", "turns 2/250", "400 in", "400 out"} {
		if !strings.Contains(view, want) {
			t.Errorf("STATS missing numeric readout %q, got: %q", want, view)
		}
	}
	if strings.Contains(view, "cost") {
		t.Errorf("STATS must not render a cost line, got: %q", view)
	}
}

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

func TestModelRailTranscriptFloor(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = asModel(t, nm)
	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible on an extreme-minimum terminal")
	}
	if tw := m.tx.transcriptWidth(); tw < 20 {
		t.Errorf("transcriptWidth = %d on a 40-col window, want the hard floor >= 20", tw)
	}
}

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

	te.updates <- TelemetryUpdate{Kind: TelemetryUsage, Hit: 90_000, Miss: 10_000, Output: 5_000}
	cmd := telemetryWait(te)
	msg := cmd()
	nm, _ = m.Update(msg)
	m = asModel(t, nm)

	if !strings.Contains(view(m), "cache 90%") {
		t.Errorf("open rail not live-updating cache gauge, got: %q", view(m))
	}
	if strings.Contains(view(m), "usage") || strings.Contains(view(m), "▁") {
		t.Errorf("open rail must not render graph rows, got: %q", view(m))
	}
}

func TestModelRailHeightMatchesHistory(t *testing.T) {
	t.Parallel()
	m := newTallHistoryModel(t)
	m.deps.Rail = NewRail("opencode-go", "deepseek-v4-flash", "high", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	m.tx.rail = m.deps.Rail
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 12})
	m = asModel(t, nm)

	if !m.tx.railVisible() {
		t.Fatal("rail must stay visible on a short window")
	}
	content := view(m)

	if n := len(strings.Split(strings.TrimRight(content, "\n"), "\n")); n > 12 {
		t.Errorf("view (%d lines) exceeds terminal height 12 with the rail open, got:\n%q", n, content)
	}
}

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
	for _, ln := range strings.Split(content, "\n") {
		if strings.Contains(ln, "Ask Eitri") && !strings.Contains(ln, "opencode-go") {
			return // composer panel starts on its own row, rail intact
		}
	}
	t.Errorf("composer panel is not on its own row; rail+composer overflow right edge:\n%q", content)
}

func TestModelRailNoPanicWithoutFeed(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Turn: fakeSess("hi")})
	m = resize(t, m)
	content := view(m)
	if strings.Contains(content, "STATS") {
		t.Errorf("nil rail must render no rail, got: %q", content)
	}
}

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
			if bw := m.tx.bandWidth(); bw != tc.w {
				t.Errorf("bandWidth = %d, want full terminal width = %d", bw, tc.w)
			}
			if m.tx.bandWidth() < 2 {
				t.Errorf("bandWidth %d must be >= 2 so the accent separator still reads as a line", m.tx.bandWidth())
			}
		})
	}
}

func TestModelBandWidthRailHiddenTiny(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Turn: fakeSess("hi")}) // no rail wired -> railVisible() == false
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 3, Height: 8})
	m = asModel(t, nm)
	if m.tx.railVisible() {
		t.Fatal("model without a wired rail must not show the rail")
	}
	if bw := m.tx.bandWidth(); bw < 1 {
		t.Errorf("rail-hidden tiny window: bandWidth = %d, want at least 1", bw)
	}
}

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

	before := m.tx.bandWidth()
	m.composer.SetWidth(200)
	after := m.tx.bandWidth()
	if before != after {
		t.Errorf("bandWidth changed %d -> %d after composer width changed; band must be independent of composer width", before, after)
	}

	twA := m.tx.transcriptWidth()
	m.composer.SetWidth(5)
	twB := m.tx.transcriptWidth()
	if twA != twB {
		t.Errorf("transcriptWidth changed %d -> %d after composer width changed; must not read composer width", twA, twB)
	}
}

func TestRailRenderCtxLine(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 137_000})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "ctx 137.0k") {
		t.Errorf("rail STATS missing ctx line, got: %q", view)
	}
	if !strings.Contains(view, "tokens") || strings.Index(view, "ctx 137.0k") < strings.Index(view, "tokens") {
		t.Errorf("ctx line must render after the tokens line, got: %q", view)
	}
	if line := lineContaining(view, "ctx 137.0k"); !strings.Contains(line, "\x1b[38;2;224;175;104m") {
		t.Errorf("ctx line below threshold = %q, want the stats hue (not warning)", line)
	}
}

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

func TestRailRenderStatsShowsContextOverflowRecovery(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryCompacted})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "recovery context overflow") {
		t.Fatalf("rail STATS missing context overflow recovery marker, got: %q", view)
	}
	if strings.Contains(view, "state compacted") {
		t.Fatalf("rail STATS still uses compaction marker, got: %q", view)
	}
}

func TestRailRenderCtxPostRecoveryRollback(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 160_000})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000, Ctx: 48_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "ctx 48.0k") {
		t.Errorf("rail STATS ctx did not roll back to 48.0k after recovery, got: %q", view)
	}
	if line := lineContaining(view, "ctx 48.0k"); strings.Contains(line, "\x1b[38;2;247;118;142m") {
		t.Errorf("ctx line after recovery %q must not carry the warning hue", line)
	}
}

func TestRailRenderStatsWide(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, 50)

	if !strings.Contains(view, "cache") {
		t.Errorf("rail STATS missing cache at wide width, got: %q", view)
	}
	if !strings.Contains(view, "turns") {
		t.Errorf("rail STATS missing turns at wide width, got: %q", view)
	}
	if !strings.Contains(view, "80%") {
		t.Errorf("rail STATS missing 80%% at wide width, got: %q", view)
	}
	if !strings.Contains(view, "1/250") {
		t.Errorf("rail STATS missing 1/250 at wide width, got: %q", view)
	}
}

func TestRailRenderModelWide(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), defaultTheme, 50)

	if !strings.Contains(view, "provider     opencode-go") || !strings.Contains(view, "model        deepseek-v4-flash") {
		t.Errorf("rail MODEL should show separated provider/model at wide width 50, got: %q", view)
	}
	if strings.Contains(view, "deepseek-v4-f…") {
		t.Errorf("rail MODEL should not truncate model at wide width 50, got: %q", view)
	}
}

func TestRailRenderStatsNarrow(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, 24)

	if !strings.Contains(view, "STATS") {
		t.Errorf("rail missing STATS at narrow width, got: %q", view)
	}
	for _, ln := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
		stripped := plain(ln)
		if w := lipgloss.Width(stripped); w > 24 {
			t.Errorf("narrow rail line %d columns wide, max %d: %q", w, 24, stripped)
		}
	}
}

func TestRailDefaultWidthUnchanged(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, defaultTheme, defaultRailWidth)

	if !strings.Contains(view, "model deepseek-v4-flash") {
		t.Errorf("rail MODEL should keep model readable at default width, got: %q", view)
	}
	if !strings.Contains(view, "cache 80%") {
		t.Errorf("rail STATS missing cache at default width, got: %q", view)
	}
	if !strings.Contains(view, "turns 1/250") {
		t.Errorf("rail STATS missing turns at default width, got: %q", view)
	}
}

func TestRailWideAlignment(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.renderStats(te, defaultTheme, 50)

	cacheLine := lineContaining(view, "80%")
	turnsLine := lineContaining(view, "1/250")
	if cacheLine == "" || turnsLine == "" {
		t.Fatalf("missing cache or turns line in wide stats: %q", view)
	}
	cachePlain := plain(cacheLine)
	turnsPlain := plain(turnsLine)
	cacheValIdx := strings.Index(cachePlain, "80%")
	turnsValIdx := strings.Index(turnsPlain, "1/250")
	if cacheValIdx != turnsValIdx {
		t.Errorf("values not aligned at wide width: cache value starts at %d, turns value starts at %d\n  cache: %q\n  turns: %q", cacheValIdx, turnsValIdx, cachePlain, turnsPlain)
	}
}

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

func TestRailWideValuesFuller(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")

	defView := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme, defaultRailWidth)
	wideView := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), defaultTheme, 55)

	if !strings.Contains(defView, "provider opencode-go") || !strings.Contains(defView, "model deepseek-v4-flash") {
		t.Errorf("default width should show separated provider/model, got: %q", defView)
	}
	if !strings.Contains(wideView, "provider       opencode-go") || !strings.Contains(wideView, "model          deepseek-v4-flash") {
		t.Errorf("wide width should show separated provider/model, got: %q", wideView)
	}
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

func TestRail_truncateCellWidthWideRunes(t *testing.T) {
	r := &Rail{}
	contentWidth := func(rail int) int { return rail - 2 }
	cases := []struct {
		key, val string
		rail     int
	}{
		{"branch", "汉字汉字汉字汉字", 10}, // plain path with all-wide value
		{"汉", "汉字汉字汉字汉字", 10},      // wide key + wide value
		{"stats", "a汉字b", 10},      // mixed narrow/wide value
	}
	for _, c := range cases {
		var b strings.Builder
		r.line(&b, c.key, c.val, c.rail)
		row := strings.TrimSuffix(b.String(), "\n")
		if got := lipgloss.Width(plain(row)); got > contentWidth(c.rail) {
			t.Errorf("line(%q,%q,%d) display width %d overflows content width %d: %q", c.key, c.val, c.rail, got, contentWidth(c.rail), row)
		}
	}
}

func TestRailStatsContextMeterStates(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek", "low", true, "sid", "/tmp/sid")
	te := NewTelemetry("deepseek", "low", true, 250)

	empty := r.renderStats(te, defaultTheme, 36)
	emptyCtx := lineContaining(empty, "ctx")
	if !strings.Contains(ansiStrip(emptyCtx), "ctx        0") || strings.Contains(ansiStrip(emptyCtx), "[") {
		t.Fatalf("empty ctx should render numeric without meter, got: %q", empty)
	}

	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Ctx: 75_000})
	normal := r.renderStats(te, defaultTheme, 36)
	if !strings.Contains(ansiStrip(normal), "ctx        75.0k [===---]") {
		t.Fatalf("normal ctx meter missing, got: %q", normal)
	}
	if strings.Contains(lineContaining(normal, "ctx 75.0k"), "38;2;247;118;142") {
		t.Fatalf("normal ctx meter used warning color: %q", lineContaining(normal, "ctx 75.0k"))
	}

	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Ctx: 150_000})
	warn := r.renderStats(te, defaultTheme, 36)
	if !strings.Contains(ansiStrip(warn), "ctx        150.0k [======]") {
		t.Fatalf("warning ctx meter missing, got: %q", warn)
	}
	warnLine := lineContaining(warn, "150.0k")
	if !strings.Contains(warnLine, "38;2;247;118;142") {
		t.Fatalf("warning ctx meter missing warning color: %q", warnLine)
	}
}

func TestRailStatsCacheMeterStates(t *testing.T) {
	t.Parallel()
	r := NewRail("opencode-go", "deepseek", "low", true, "sid", "/tmp/sid")
	for _, tc := range []struct {
		name string
		hit  int
		miss int
		want string
	}{
		{"zero", 0, 10, "0% [------]"},
		{"partial", 4, 6, "40% [==----]"},
		{"full", 10, 0, "100% [======]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := NewTelemetry("deepseek", "low", true, 250)
			te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: tc.hit, Miss: tc.miss})
			view := r.renderStats(te, defaultTheme, 36)
			if !strings.Contains(ansiStrip(view), tc.want) {
				t.Fatalf("cache meter missing %q, got: %q", tc.want, view)
			}
		})
	}
}
