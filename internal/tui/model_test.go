package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_greetingRoundTrip(t *testing.T) {
	t.Parallel()
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		if prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", prompt)
		}
		return TurnResult{Answer: "Hello! **glad** to help."}, nil
	})

	m = resize(t, m)

	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	content := view(m)
	if !strings.Contains(content, "hello") || !strings.Contains(content, "glad") {
		t.Errorf("expected prompt and answer in view, got: %q", content)
	}
	if strings.Contains(content, "you") || strings.Contains(content, "eitri") {
		t.Errorf("role labels must not render in the transcript, got: %q", content)
	}
	if !strings.Contains(content, "glad") {
		t.Errorf("expected assistant answer text in view, got: %q", content)
	}
	if !hasSGRBold(view(m)) {
		t.Errorf("expected markdown bold to render in assistant answer, got: %q", view(m))
	}
	if strings.Contains(content, "\x1b[2J") {
		t.Errorf("view carries a clear-screen sequence; must render to primary buffer")
	}
	if strings.Contains(content, "\x1b[?1049") {
		t.Errorf("view carries an alt-screen sequence; must render to primary buffer")
	}
}

func TestModel_errorTurn(t *testing.T) {
	t.Parallel()
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{}, errors.New("provider exploded")
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	if !strings.Contains(view(m), "provider") || !strings.Contains(view(m), "exploded") {
		t.Errorf("expected error words in view, got: %q", view(m))
	}
}

func TestModel_thinkingCollapsible(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{
				Answer:    "plain answer",
				Reasoning: "I reason about it first.",
			}, nil
		},
		Config: config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	content := view(m)
	if !strings.Contains(content, "🤔") {
		t.Errorf("expected a thinking hint in content, got: %q", content)
	}
	if strings.Contains(content, "I reason about it first") {
		t.Errorf("reasoning body should be collapsed by default, got: %q", content)
	}

	toggled, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = asModel(t, toggled)
	expanded := view(m)
	if !strings.Contains(expanded, "I reason about it first") {
		t.Errorf("tab should expand the reasoning block, got: %q", expanded)
	}
	if !strings.Contains(expanded, "plain") {
		t.Errorf("answer still required in content, got: %q", expanded)
	}
	thinkingIdx := strings.Index(expanded, "I reason about it first")
	answerIdx := strings.Index(expanded, "plain")
	if thinkingIdx == -1 || answerIdx == -1 || thinkingIdx > answerIdx {
		t.Errorf("reasoning block must render as its own stream before the answer, got: %q", expanded)
	}
}

func TestModel_thinkingHintReportsTokensAndEffort(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: strings.Repeat("reasoning words. ", 400)}, nil
		},
		Config: config.Config{ThinkingEnabled: true, ReasoningEffort: "medium"},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	content := view(m)
	if !strings.Contains(content, "🤔") {
		t.Errorf("collapsed state should show the thinking hint, got: %q", content)
	}
	if !strings.Contains(content, "· medium") {
		t.Errorf("hint should carry the reasoning-effort tier, got: %q", content)
	}
	if !strings.Contains(content, "tok") {
		t.Errorf("hint should carry a token count, got: %q", content)
	}
	if strings.Contains(content, "reasoning words") {
		t.Errorf("reasoning body must stay collapsed behind the hint, got: %q", content)
	}
}

func TestModel_thinkingAutoCollapsesOnAnswer(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "hidden reasoning")
	if !strings.Contains(view(m), "hidden reasoning") {
		t.Errorf("live block should show reasoning before answer lands, got: %q", view(m))
	}
	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "final answer", reasoning: "hidden reasoning"})
	m = asModel(t, nm)
	if strings.Contains(view(m), "hidden reasoning") {
		t.Errorf("thinking block should auto-collapse when the answer lands, got: %q", view(m))
	}
}

func TestModel_railRendersNoSkillsSection(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "my-skill"}}},
		Rail:   NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1"),
	})
	m = resizeTo(t, m, 140, 24)
	content := view(m)
	for _, want := range []string{"STATS", "CONTEXT", "MODEL"} {
		if !strings.Contains(content, want) {
			t.Errorf("rail missing %s section, got: %q", want, content)
		}
	}
	if strings.Contains(content, "SKILLS") {
		t.Errorf("rail must not render a SKILLS section, got: %q", content)
	}
	if strings.Contains(content, "my-skill") {
		t.Errorf("rail must not list detected skills, got: %q", content)
	}
}

func TestModel_slashCommandActivatesSkill(t *testing.T) {
	t.Parallel()
	var activated string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				activated = name
				return `<skill_content name="my-skill">payload</skill_content>`, nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill")
	m = submitAndWait(t, m)

	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	content := view(m)
	if !strings.Contains(content, "payload") {
		t.Errorf("skill payload should render in content, got: %q", content)
	}
}

func TestModel_slashCompletionTab(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: "alpha"},
			{Name: "beta"},
		}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/a")
	toggled, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = asModel(t, toggled)
	if got := m.composer.Value(); got != "/alpha" {
		t.Errorf("tab completion = %q, want \"/alpha\"", got)
	}
}
func resize(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return asModel(t, nm)
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: s})
	return asModel(t, nm)
}

func submitAndWait(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	out := asModel(t, nm)
	return runSubmitted(t, out, cmd)
}

func runSubmitted(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, c := range bm {
			m = runSubmitted(t, m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	nm, next := m.Update(msg)
	m = asModel(t, nm)
	return runSubmitted(t, m, next)
}

func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	md, ok := tm.(Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want Model", tm)
	}
	return md
}

func view(m Model) string { return m.View().Content }

func TestModel_shiftEnterInsertsNewline(t *testing.T) {
	t.Parallel()
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		t.Fatalf("Shift+Enter must not submit a turn, got prompt %q", prompt)
		return TurnResult{}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")

	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)

	if got := m.composer.Value(); got != "line one\n" {
		t.Errorf("Shift+Enter should insert a newline, composer = %q", got)
	}
	if m.tx.busy {
		t.Errorf("Shift+Enter must not mark the model busy")
	}
}

func TestModel_workspaceStateSurfaced(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: "/tmp/acme-project",
	})
	m = resize(t, m)

	content := view(m)
	if !strings.Contains(content, "/tmp/acme-project") {
		t.Errorf("expected workspace path surfaced in view (issue #82 AC1), got: %q", content)
	}

	if strings.Contains(m.composer.Value(), "/tmp/acme-project") {
		t.Errorf("workspace path must not leak into the composer input, got: %q", m.composer.Value())
	}

	bare := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	bare = resize(t, bare)
	if strings.Contains(view(bare), "workspace:") {
		t.Errorf("expected no workspace header when none is configured (issue #82 AC1)")
	}
}

func TestModel_shiftEnterThenSubmitSendsWholeMultiLine(t *testing.T) {
	t.Parallel()
	var got []string
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")
	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)
	m = typeText(t, m, "line two")

	if m.tx.busy {
		t.Fatalf("composing must keep the model idle until submit")
	}

	m = submitAndWait(t, m)
	if len(got) != 1 {
		t.Fatalf("expected one turn, engine saw %d", len(got))
	}
	if got[0] != "line one\nline two" {
		t.Errorf("engine should receive the whole multi-line prompt, got %q", got[0])
	}
}

func TestModel_shiftEnterIgnoredWhileBusy(t *testing.T) {
	t.Parallel()
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "first")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a turn command on submit")
	}
	m = asModel(t, nm)

	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)
	if got := m.composer.Value(); got != "" {
		t.Errorf("Shift+Enter while busy should not edit the composer, got %q", got)
	}
}
