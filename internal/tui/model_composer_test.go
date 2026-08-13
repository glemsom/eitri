package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_enterSubmitsAndClearsComposer asserts the plain Enter key submits
// the current prompt and clears the composer back to a single empty row —
// never inserting a newline into the draft (issue #121 AC1). The submit path
// (turn command + busy flag) is the existing behaviour and must be preserved.
func TestModel_enterSubmitsAndClearsComposer(t *testing.T) {
	var got []string
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")

	m = submitAndWait(t, m)

	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Enter should submit the draft, engine saw %q", got)
	}
	if v := m.composer.Value(); v != "" {
		t.Errorf("composer must be cleared after submit, value = %q", v)
	}
	if h := m.composer.Height(); h != 1 {
		t.Errorf("cleared composer should sit at 1 row, got %d", h)
	}
}

// TestModel_composerGrowsWithDraftLines asserts the composer grows within the
// bottom band as the draft gains lines, one row per hard newline, up to the
// maxComposerRows bound (issue #121 AC5): a short draft stays compact instead
// of the fixed-height composer of the pre-pivot TUI, and an over-long draft
// caps at the bound rather than growing without limit.
func TestModel_composerGrowsWithDraftLines(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)

	if h := m.composer.Height(); h != 1 {
		t.Fatalf("empty composer should sit at 1 row, got %d", h)
	}

	m = typeText(t, m, "line one")
	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)
	m = typeText(t, m, "line two")
	if h := m.composer.Height(); h != 2 {
		t.Errorf("two-line draft should grow the composer to 2 rows, got %d", h)
	}

	// Push the draft far past the bound: the composer must cap, never exceed.
	for i := 0; i < maxComposerRows+4; i++ {
		m = typeText(t, m, "draft")
		newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		m = asModel(t, newlined)
	}
	if h := m.composer.Height(); h != maxComposerRows {
		t.Errorf("draft beyond the bound should cap the composer at %d rows, got %d", maxComposerRows, h)
	}
}

// TestModel_composerGrowsForSoftWrappedLines asserts the composer also grows
// for soft-wrapped rows: a single hard line wider than the composer wraps
// across several terminal rows, and the composer tracks them so the draft is
// visible without clipping (issue #121 AC5).
func TestModel_composerGrowsForSoftWrappedLines(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m) // 80 cols -> composer width 78

	// 200 ASCII chars -> ceil(200/78) = 3 wrapped rows.
	m = typeText(t, m, strings.Repeat("word ", 40))
	if h := m.composer.Height(); h != 3 {
		t.Errorf("200-char draft at 78 cols should grow the composer to 3 rows, got %d (width %d)", h, m.composer.Width())
	}
}

// TestModel_composerLongDraftBandPinned asserts an over-bound draft never
// pushes the transcript or band off-screen (issue #121 AC5/AC6): the composer
// caps at maxComposerRows and scrolls internally (its rendered rows never
// exceed the cap), the band stays the bottom-pinned last region, and the total
// view never exceeds the terminal height — the history viewport yields rows
// instead.
func TestModel_composerLongDraftBandPinned(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
	})
	m = resizeTo(t, m, 80, 12)

	// Grow the draft far beyond the bound (band = status strip + composer).
	for i := 0; i < maxComposerRows+10; i++ {
		m = typeText(t, m, "draft line")
		newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		m = asModel(t, newlined)
	}

	// The composer renders at most the bound: taller drafts scroll internally.
	comp := m.composer.View()
	compLines := strings.Split(strings.TrimRight(comp, "\n"), "\n")
	if len(compLines) != maxComposerRows {
		t.Errorf("over-bound draft should render %d composer rows (internal scroll), got %d", maxComposerRows, len(compLines))
	}

	content := view(m)
	trimmed := strings.TrimRight(content, "\n")
	// The band (status strip + composer) is the last region: after it there is
	// only whitespace.
	if !strings.HasSuffix(trimmed, strings.TrimRight(comp, "\n")) {
		t.Errorf("band must stay pinned at the bottom with an over-bound draft, got:\n%q", content)
	}
	// The whole content never exceeds the terminal: nothing is pushed off-screen.
	if n := len(strings.Split(trimmed, "\n")); n > 12 {
		t.Errorf("view (%d lines) exceeds terminal height 12 with an over-bound draft, got:\n%q", n, trimmed)
	}
}

// TestModel_statusAndSlashPinnedAboveComposer asserts the status strip and the
// slash-completion list stay pinned above the composer regardless of composer
// height (issue #121 AC6): with a telemetry strip, a `/...` partial, and a
// soft-wrapped grown composer all present, the content order is status strip,
// slash completion, then the composer as the final region.
func TestModel_statusAndSlashPinnedAboveComposer(t *testing.T) {
	// A skill name long enough that its `/`-partial soft-wraps the composer to
	// two rows while still completing: the slash list must stay above the
	// grown composer.
	skillName := strings.Repeat("x", 100)
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: skillName, Description: "d", Scope: "user"},
		}},
	})
	// Width 100: wide enough for the full status strip (collapseWidth), narrow
	// enough that the 101-char `/`-partial soft-wraps (composer width 98).
	m = resizeTo(t, m, 100, 24)
	m = typeText(t, m, "/"+skillName)

	content := view(m)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	comp := m.composer.View()
	compLines := strings.Split(strings.TrimRight(comp, "\n"), "\n")
	if len(compLines) < 2 {
		t.Fatalf("precondition: grown composer should span >= 2 rows, got %d", len(compLines))
	}

	// The composer is the final region.
	compStart := len(lines) - len(compLines)
	if !strings.HasSuffix(strings.TrimRight(content, "\n"), strings.TrimRight(comp, "\n")) {
		t.Fatalf("composer must be the bottom region, got:\n%q", content)
	}

	// Find the status strip row (the turns readout — the strip renders its
	// collapsed form at the 98-col composer width) and the slash
	// completion row (the ▸ marker of the candidate list); both above the
	// composer block.
	statusIdx, slashIdx := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "0/250") && statusIdx == -1 {
			statusIdx = i
		}
		if strings.Contains(ln, "▸") && slashIdx == -1 {
			slashIdx = i
		}
	}
	if statusIdx == -1 {
		t.Errorf("status strip missing from content, got:\n%q", content)
	} else if statusIdx >= compStart {
		t.Errorf("status strip must stay above the composer (status %d, composer %d)", statusIdx, compStart)
	}
	if slashIdx == -1 {
		t.Errorf("slash completion missing from content, got:\n%q", content)
	} else if slashIdx >= compStart {
		t.Errorf("slash completion must stay above the composer (slash %d, composer %d)", slashIdx, compStart)
	}
}
