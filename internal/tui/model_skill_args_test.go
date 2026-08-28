package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_slashSkillWithArgs(t *testing.T) {
	t.Parallel()
	var activated string
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "done"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				activated = name
				return "skill-note-payload", nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill improve this")
	submitAndWait(t, m)

	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	if len(turnPrompts) != 1 || turnPrompts[0] != "improve this" {
		t.Errorf("args turn seam called with %v, want [\"improve this\"]", turnPrompts)
	}
	content := ansiStrip(view(m))
	if !strings.Contains(content, "improve this") {
		t.Errorf("args message should render, got: %q", content)
	}
	if strings.Contains(content, "skill-note-payload") {
		t.Errorf("skill payload must be delivered once via injection, not echoed as an assistant note, got: %q", content)
	}
}

func TestModel_slashSkillWithMultiWordArgs(t *testing.T) {
	t.Parallel()
	want := "Let us improve this codebase"
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				return "note", nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill "+want)
	submitAndWait(t, m)

	if len(turnPrompts) != 1 || turnPrompts[0] != want {
		t.Errorf("multi-word args in turn seam = %v, want [%q]", turnPrompts, want)
	}
}

func TestModel_slashSkillNoArgs(t *testing.T) {
	t.Parallel()
	var activated string
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				activated = name
				return "payload-note", nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill")
	m = submitAndWait(t, m)

	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	if len(turnPrompts) != 1 || turnPrompts[0] != "apply the my-skill skill" {
		t.Errorf("bare skill turn seam = %v, want [\"apply the my-skill skill\"]", turnPrompts)
	}
	if strings.Contains(view(m), "payload-note") {
		t.Errorf("skill payload must be delivered once via injection, not echoed as an assistant note, got: %q", view(m))
	}
}

func TestModel_slashSkillActivationBlocksConcurrentSubmit(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(ctx context.Context, name string) (string, error) {
				select {
				case <-release:
					return "payload", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill improve this")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatal("skill activation command = nil, want pending command")
	}
	if !m.tx.busy {
		t.Fatal("skill activation must mark transcript busy while payload loads")
	}

	m = typeText(t, m, "normal prompt")
	nm, blocked := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, nm)
	if blocked != nil {
		t.Fatalf("busy skill activation accepted concurrent prompt command %#v", blocked)
	}
	if len(turnPrompts) != 0 {
		t.Fatalf("turn prompts while skill pending = %v, want none", turnPrompts)
	}

	close(release)
	m = runSubmitted(t, m, cmd)
	if len(turnPrompts) != 1 || turnPrompts[0] != "improve this" {
		t.Fatalf("turn prompts = %v, want [improve this]", turnPrompts)
	}
}

func TestModel_slashSkillActivationError(t *testing.T) {
	t.Parallel()
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				return "", errors.New("boom activation failed")
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill some args")
	m = submitAndWait(t, m)

	if len(turnPrompts) != 0 {
		t.Errorf("error path must not dispatch a turn, got %v", turnPrompts)
	}
	content := view(m)
	if !strings.Contains(content, "boom") || !strings.Contains(content, "failed") {
		t.Errorf("activation failure should render, got: %q", content)
	}
}
