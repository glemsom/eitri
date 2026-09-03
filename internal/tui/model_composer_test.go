package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_enterSubmitsAndClearsComposer(t *testing.T) {
	t.Parallel()
	var got []string
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	}})
	m = resize(t, m)
	m = typeText(t, m, "hello")

	m = submitAndWait(t, m)

	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Enter should submit the draft, engine saw %q", got)
	}
	if v := m.composer.Value(); v != "" {
		t.Errorf("composer must be cleared after submit, value = %q", v)
	}
	if h := m.composer.Height(); h != minComposerRows {
		t.Errorf("cleared composer should return to its %d-row resting height, got %d", minComposerRows, h)
	}
}

func TestModel_shiftEnterInsertsNewlineWithoutSubmitting(t *testing.T) {
	t.Parallel()
	var got []string
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	}})
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

func TestModel_shiftEnterCsiUInsertsNewline(t *testing.T) {
	t.Parallel()
	var got []string
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	}})
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

func newlineShiftEnterCsiU(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	return asModel(t, nm)
}

func TestModel_composerMultiLineInsertAndSubmit(t *testing.T) {
	t.Parallel()
	var got []string
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	}})
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
	if h := m.composer.Height(); h != minComposerRows {
		t.Errorf("cleared composer should return to its %d-row resting height, got %d", minComposerRows, h)
	}
}

func newlineShiftEnter(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	return asModel(t, nm)
}

func TestModel_composerGrowsWithDraftLines(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}})
	m = resize(t, m)

	if h := m.composer.Height(); h != minComposerRows {
		t.Fatalf("empty composer should rest at %d rows, got %d", minComposerRows, h)
	}
	if rows := composerRows(m); len(rows) != minComposerRows {
		t.Errorf("empty composer should render %d panel body rows, got %d", minComposerRows, len(rows))
	}

	m = typeText(t, m, "line one")
	m = newlineShiftEnter(t, m)
	m = typeText(t, m, "line two")
	if h := m.composer.Height(); h != 2 {
		t.Errorf("two-line draft should hold the composer at 2 rows, got %d", h)
	}
	m = newlineShiftEnter(t, m)
	m = typeText(t, m, "line three")
	if h := m.composer.Height(); h != 3 {
		t.Errorf("three-line draft should grow the composer past the resting height to 3 rows, got %d", h)
	}

	for i := 0; i < maxComposerRows+4; i++ {
		m = typeText(t, m, "draft")
		m = newlineShiftEnter(t, m)
	}
	if h := m.composer.Height(); h != maxComposerRows {
		t.Errorf("draft beyond the bound should cap the composer at %d rows, got %d", maxComposerRows, h)
	}
}

func TestModel_composerGrowsForSoftWrappedLines(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}})
	m = resize(t, m) // 80 cols -> composer width 78

	m = typeText(t, m, strings.Repeat("word ", 40))
	if h := m.composer.Height(); h != 3 {
		t.Errorf("200-char draft at 78 cols should grow the composer to 3 rows, got %d (width %d)", h, m.composer.Width())
	}
}

func TestModel_composerLongDraftBandPinned(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
	})
	m = resizeTo(t, m, 80, 12)

	for i := 0; i < maxComposerRows+10; i++ {
		m = typeText(t, m, "draft line")
		m = newlineShiftEnter(t, m)
	}

	comp := m.composer.View()
	compLines := strings.Split(strings.TrimRight(comp, "\n"), "\n")
	if len(compLines) != maxComposerRows {
		t.Errorf("over-bound draft should render %d composer rows (internal scroll), got %d", maxComposerRows, len(compLines))
	}

	content := view(m)
	trimmed := strings.TrimRight(content, "\n")
	plain := ansiStrip(trimmed)
	if !strings.Contains(plain, "Ask Eitri") || !strings.Contains(plain, "enter send") {
		t.Errorf("band must stay pinned at the bottom with the composer panel and hint row, got:\n%q", content)
	}
	if n := len(strings.Split(trimmed, "\n")); n > 12 {
		t.Errorf("view (%d lines) exceeds terminal height 12 with an over-bound draft, got:\n%q", n, trimmed)
	}
}

func TestModel_composerShortTerminalClamp(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resizeTo(t, m, 80, 2)

	content := view(m)
	if n := len(strings.Split(strings.TrimRight(content, "\n"), "\n")); n > 2 {
		t.Errorf("composer pushed the band off a 2-row terminal: view is %d rows:\n%q", n, content)
	}
}

func TestModel_statusAndSlashPinnedAboveComposer(t *testing.T) {
	t.Parallel()
	skillName := strings.Repeat("x", 100)
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: skillName},
		}},
	})
	m = resizeTo(t, m, 100, 24)
	m = typeText(t, m, "/"+skillName)

	content := view(m)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	comp := m.composer.View()
	compLines := strings.Split(strings.TrimRight(comp, "\n"), "\n")
	if len(compLines) < 2 {
		t.Fatalf("precondition: grown composer should span >= 2 rows, got %d", len(compLines))
	}

	compStart := -1
	for i, ln := range lines {
		if strings.Contains(ln, "Ask Eitri") {
			compStart = i
			break
		}
	}
	if compStart == -1 {
		t.Fatalf("composer panel missing from content, got:\n%q", content)
	}
	compEnd := compStart + len(compLines) + 2 // titled panel borders around textarea rows
	if compEnd > len(lines) {
		t.Fatalf("composer panel extends past rendered content (start %d, composer rows %d, total %d):\n%q", compStart, len(compLines), len(lines), content)
	}
	if !strings.Contains(ansiStrip(lines[compEnd-1]), "╯") {
		t.Fatalf("composer panel must end with its bottom border, got:\n%q", content)
	}

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
