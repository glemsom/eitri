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
// telemetry: cache hit %, cost, turns, and token in/out (issue #88).
func TestRailRenderStats(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c1a", "/tmp/eitri-9f2c1a")
	view := r.render(te, nil)

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
	// 100k hit + 25k miss = 125k in; 10k out.
	if !strings.Contains(view, "125.0k in") || !strings.Contains(view, "10.0k out") {
		t.Errorf("rail STATS missing token in/out, got: %q", view)
	}
}

// TestRailRenderModel asserts the MODEL section reflects the session's static
// provider/model/effort/thinking (issue #88).
func TestRailRenderModel(t *testing.T) {
	r := NewRail("opencode-go", "deepseek-v4-flash", "high", false, "sess-1", "/tmp/sess-1")
	view := r.render(NewTelemetry("deepseek-v4-flash", "high", false, 250), nil)

	if !strings.Contains(view, "MODEL") {
		t.Errorf("rail missing MODEL section, got: %q", view)
	}
	if !strings.Contains(view, "opencode-go/deepseek-v4-flash") {
		t.Errorf("rail MODEL missing provider/model, got: %q", view)
	}
	if !strings.Contains(view, "effort high") {
		t.Errorf("rail MODEL missing effort, got: %q", view)
	}
	if !strings.Contains(view, "thinking off") {
		t.Errorf("rail MODEL missing thinking flag, got: %q", view)
	}
}

// TestRailRenderContext asserts the CONTEXT section reflects the session id
// and session temp path, and the SKILLS section lists detected skills with
// their scope + activation state (issue #88).
func TestRailRenderContext(t *testing.T) {
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-9f2c", "/tmp/eitri-9f2c")
	skills := []SkillItem{
		{Name: "go-guidelines", Scope: "user", Active: true},
		{Name: "security-review", Scope: "project", Active: false},
	}
	view := r.render(NewTelemetry("deepseek-v4-flash", "low", true, 250), skills)

	if !strings.Contains(view, "CONTEXT") {
		t.Errorf("rail missing CONTEXT section, got: %q", view)
	}
	if !strings.Contains(view, "SKILLS") {
		t.Errorf("rail missing SKILLS section, got: %q", view)
	}
	if !strings.Contains(view, "go-guidelines [user] ✓") {
		t.Errorf("rail SKILLS missing active skill row, got: %q", view)
	}
	if !strings.Contains(view, "security-review [project] ✕") {
		t.Errorf("rail SKILLS missing inactive skill row, got: %q", view)
	}
	if !strings.Contains(view, "session eitri-9f2c") {
	}
	if !strings.Contains(view, "session eitri-9f2c") {
		t.Errorf("rail CONTEXT missing session id, got: %q", view)
	}
	if !strings.Contains(view, "temp /tmp/eitri-9f2c") {
		t.Errorf("rail CONTEXT missing session temp path, got: %q", view)
	}
}

// TestModelRailToggles asserts ctrl+b toggles the rail between visible and
// hidden on any width without disturbing the transcript (issue #88 AC1).
func TestModelRailToggles(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn: fakeSess("hi"),
		// Wide window: default auto-shows the rail.
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m = asModel(t, nm)

	if !m.railVisible() {
		t.Fatal("rail should auto-show on a wide window")
	}

	// ctrl+b hides it.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if m.railVisible() {
		t.Fatal("ctrl+b should hide the rail on a wide window")
	}
	// ctrl+b again shows it.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("ctrl+b should re-show the rail")
	}
}

// TestModelRailAutoHidesShort asserts the rail auto-shows only when the
// terminal is tall enough to host it beside the fixed bottom band, alongside
// the width gate (issue T05 AC2): a wide-but-short window
// auto-hides the rail just like a narrow one. ctrl+b still forces it open on
// any size.
func TestModelRailAutoHidesShort(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 10})
	m = asModel(t, nm)

	if m.railVisible() {
		t.Fatal("rail must auto-hide on a wide-but-short window")
	}
	// ctrl+b still opens it on any size.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("ctrl+b must open the rail on a short window")
	}
	if !strings.Contains(view(m), "STATS") {
		t.Errorf("open rail missing STATS section, got: %q", view(m))
	}
}

// TestModelRailAutoHidesNarrow asserts the rail auto-hides below ~120 cols so
// the primary buffer keeps full-width selection (issue #88 AC3), and ctrl+b
// still forces it open on a narrow window.
func TestModelRailAutoHidesNarrow(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: te,
		Rail:      r,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = asModel(t, nm)

	if m.railVisible() {
		t.Fatal("rail must auto-hide on a narrow window")
	}
	// ctrl+b opens it on any width.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if !m.railVisible() {
		t.Fatal("ctrl+b must open the rail on a narrow window")
	}
	content := view(m)
	if !strings.Contains(content, "STATS") {
		t.Errorf("open rail missing STATS section, got: %q", content)
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

	// Force it open and feed a live usage update.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	nm, _ = nm.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	te.updates <- TelemetryUpdate{Kind: TelemetryUsage, Hit: 90_000, Miss: 10_000, Output: 5_000}
	cmd := telemetryWait(te)
	msg := cmd()
	nm, _ = m.Update(msg)
	m = asModel(t, nm)

	if !strings.Contains(view(m), "cache 90%") {
		t.Errorf("open rail not live-updating cache gauge, got: %q", view(m))
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
	// ctrl+b forces the rail open on any size (AC3); auto-show would gate on
	// the short height.
	nm, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = asModel(t, nm)

	if !m.railVisible() {
		t.Fatal("ctrl+b must force the rail open")
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
		t.Fatal("rail should auto-show at 120x30")
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
