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

// TestModel_shiftEnterInsertsNewlineWithoutSubmitting asserts Shift+Enter
// (surfaced by Bubble Tea as the line-feed key, KeyCtrlJ) inserts a line
// break into the draft instead of submitting it (issue #121 AC2): after the
// key the turn seam has seen nothing and the composer holds the two-line
// draft.
func TestModel_shiftEnterInsertsNewlineWithoutSubmitting(t *testing.T) {
	var got []string
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")
	m = newlineShiftEnter(t, m)
	m = typeText(t, m, "line two")

	if len(got) != 0 {
		t.Fatalf("Shift+Enter must not submit; engine saw %q", got)
	}
	if v := m.composer.Value(); v != "line one\nline two" {
		t.Errorf("Shift+Enter should insert a newline into the draft, value = %q", v)
	}
	if h := m.composer.Height(); h != 2 {
		t.Errorf("two-line draft should sit at 2 rows, got %d", h)
	}
}

// TestModel_shiftEnterCsiUInsertsNewline asserts Shift+Enter delivered through
// the enhanced (CSI u / kitty) keyboard protocol — decoded by Bubble Tea v2 as
// Code KeyEnter with ModShift, String() "shift+enter" — inserts a line break
// instead of submitting (issue #121 AC2). Terminals with keyboard
// enhancements enabled (kitty, WezTerm, ghostty, Windows Terminal) send CSI u
// for Shift+Enter rather than the legacy line-feed byte, so the plain "ctrl+j"
// case never sees it and the key currently falls through as a no-op.
func TestModel_shiftEnterCsiUInsertsNewline(t *testing.T) {
	var got []string
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")
	m = newlineShiftEnterCsiU(t, m)
	m = typeText(t, m, "line two")

	if len(got) != 0 {
		t.Fatalf("Shift+Enter must not submit; engine saw %q", got)
	}
	if v := m.composer.Value(); v != "line one\nline two" {
		t.Errorf("Shift+Enter should insert a newline into the draft, value = %q", v)
	}
	if h := m.composer.Height(); h != 2 {
		t.Errorf("two-line draft should sit at 2 rows, got %d", h)
	}
}

// newlineShiftEnterCsiU drives the Shift+Enter newline key as delivered by the
// enhanced keyboard protocol: KeyEnter with the shift modifier held.
func newlineShiftEnterCsiU(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	return asModel(t, nm)
}

// TestModel_composerMultiLineInsertAndSubmit asserts the full multi-line
// composer cycle (issue #126 AC3 / #121 AC2): Shift+Enter builds a two-line
// draft, plain Enter submits it verbatim to the engine seam, and the composer
// clears back to one row. The newlines the user typed must reach the turn
// seam, not be flattened or dropped.
func TestModel_composerMultiLineInsertAndSubmit(t *testing.T) {
	var got []string
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")
	m = newlineShiftEnter(t, m)
	m = typeText(t, m, "line two")

	m = submitAndWait(t, m)

	if len(got) != 1 || got[0] != "line one\nline two" {
		t.Fatalf("multi-line draft must submit verbatim, engine saw %q", got)
	}
	if v := m.composer.Value(); v != "" {
		t.Errorf("composer must be cleared after multi-line submit, value = %q", v)
	}
	if h := m.composer.Height(); h != 1 {
		t.Errorf("cleared composer should sit at 1 row, got %d", h)
	}
}

// newlineShiftEnter drives the Shift+Enter newline key (surfaced as KeyCtrlJ)
// through the model's Update seam.
func newlineShiftEnter(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	return asModel(t, nm)
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
	m = newlineShiftEnter(t, m)
	m = typeText(t, m, "line two")
	if h := m.composer.Height(); h != 2 {
		t.Errorf("two-line draft should grow the composer to 2 rows, got %d", h)
	}

	// Push the draft far past the bound: the composer must cap, never exceed.
	for i := 0; i < maxComposerRows+4; i++ {
		m = typeText(t, m, "draft")
		m = newlineShiftEnter(t, m)
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
		m = newlineShiftEnter(t, m)
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
			{Name: skillName},
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

	// Find the status strip row (the keybinding hints — the bottom band's
	// status line is hints-only now, issue #228) and the slash
	// completion row (the ▸ marker of the candidate list); both above the
	// composer block.
	statusIdx, slashIdx := -1, -1
	for i, ln := range lines {
		if strings.Contains(ln, "ctrl+s settings") && statusIdx == -1 {
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
