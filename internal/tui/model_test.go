package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModel_greetingRoundTrip drives a greeting through the model over the
// injected Turn seam (the engine stand-in) and asserts the assistant answer is
// appended to the conversation (ticket #34: "a TUI run of a greeting
// round-trips through the engine and renders the answer").
func TestModel_greetingRoundTrip(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		if prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", prompt)
		}
		return TurnResult{Answer: "Hello! **glad** to help."}, nil
	})

	// Set a size so the composer has a width.
	m = resize(t, m)

	// Focus + type the prompt, then submit.
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	view := m.View()
	if !strings.Contains(view, "you") || !strings.Contains(view, "eitri") {
		t.Errorf("expected both role headers in view, got: %q", view)
	}
	// The assistant's markdown answer must render (bold "glad" carries SGR 1).
	if !strings.Contains(view, "glad") {
		t.Errorf("expected assistant answer text in view, got: %q", view)
	}
	if !hasSGRBold(m.View()) {
		t.Errorf("expected markdown bold to render in assistant answer, got: %q", m.View())
	}
	// The TUI renders into the primary buffer by default: the conversation view
	// must never carry a clear-screen (\x1b[2J) or alt-screen (\x1b[?1049)
	// sequence, so native scrollback/selection/search survive (docs/spec.md §9).
	if strings.Contains(view, "\x1b[2J") {
		t.Errorf("view carries a clear-screen sequence; must render to primary buffer")
	}
	if strings.Contains(view, "\x1b[?1049") {
		t.Errorf("view carries an alt-screen sequence; must render to primary buffer")
	}
}

// TestModel_errorTurn asserts a failing turn renders a visible error instead of
// silently dropping.
func TestModel_errorTurn(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{}, errors.New("provider exploded")
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	if !strings.Contains(m.View(), "provider") || !strings.Contains(m.View(), "exploded") {
		t.Errorf("expected error words in view, got: %q", m.View())
	}
}

// resize installs a window size on the model.

// TestModel_thinkingCollapsible asserts reasoning renders as a distinct,
// auto-collapsed block: the header is always shown, the reasoning body is
// hidden until `tab` expands it, and reasoning never leaks into the answer
// (docs/spec.md §6, ticket #17).
func TestModel_thinkingCollapsible(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{
			Answer:    "plain answer",
			Reasoning: "I reason about it first.",
		}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	// Auto-collapsed after the turn: header present, body absent by default.
	view := m.View()
	if !strings.Contains(view, "thinking") {
		t.Errorf("expected a thinking header in view, got: %q", view)
	}
	if strings.Contains(view, "I reason about it first") {
		t.Errorf("reasoning body should be collapsed by default, got: %q", view)
	}

	// Toggling with `tab` expands the reasoning body.
	toggled, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = asModel(t, toggled)
	expanded := m.View()
	if !strings.Contains(expanded, "I reason about it first") {
		t.Errorf("tab should expand the reasoning body, got: %q", expanded)
	}
	// The answer is still rendered, and reasoning appears before it as a distinct
	// stream (never interleaved into the answer body). Glamour word-wraps the
	// answer across ANSI runs, so match on the word rather than the full phrase.
	if !strings.Contains(expanded, "plain") {
		t.Errorf("answer still required in view, got: %q", expanded)
	}
	thinkingIdx := strings.Index(expanded, "I reason about it first")
	eitriIdx := strings.Index(expanded, "eitri")
	if thinkingIdx == -1 || eitriIdx == -1 || thinkingIdx > eitriIdx {
		t.Errorf("reasoning block must render as its own stream before the answer, got: %q", expanded)
	}
}

// TestModel_skillsPanelRenders asserts the skills panel lists detected skills
// with their install scope and activation state (docs/spec.md §9, eitri.md
// §2.3).
func TestModel_skillsPanelRenders(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "my-skill", Description: "a demo", Scope: "project"}}},
	})
	m = resize(t, m)
	view := m.View()
	if !strings.Contains(view, "skills") {
		t.Errorf("expected a skills panel header, got: %q", view)
	}
	if !strings.Contains(view, "my-skill") || !strings.Contains(view, "project") {
		t.Errorf("expected detected skill + scope in panel, got: %q", view)
	}
}

// TestModel_slashCommandActivatesSkill drives `/skillname` through the TUI
// slash-command path: the activation seam runs, the skill is marked active in
// the panel, and the activation payload renders as an assistant note. This is
// the TUI side of the engine-seam activation flow (ticket #35).
func TestModel_slashCommandActivatesSkill(t *testing.T) {
	var activated string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill", Description: "a demo", Scope: "user"}},
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
	view := m.View()
	if !strings.Contains(view, "payload") {
		t.Errorf("skill payload should render in view, got: %q", view)
	}
	// The activated skill shows ✓ in the panel.
	if !strings.Contains(view, "✓") {
		t.Errorf("activated skill should be marked active in panel, got: %q", view)
	}
}

// TestModel_slashCompletionTab asserts `/` + tab cycles a skill-name completion
// in the composer (eitri.md §2.3).
func TestModel_slashCompletionTab(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: "alpha", Scope: "user"},
			{Name: "beta", Scope: "project"},
		}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/a")
	// Tab completes the partial `/a` to `/alpha`.
	toggled, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
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

// typeText feeds the given runes to the composer in one keypress.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return asModel(t, nm)
}

// submitAndWait feeds Enter to run the turn and then the async completion.
func submitAndWait(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	out := asModel(t, nm)
	// Run the command to completion synchronously: it returns the done message.
	done := cmd()
	out2, _ := out.Update(done)
	return asModel(t, out2)
}

func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	md, ok := tm.(Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want Model", tm)
	}
	return md
}
