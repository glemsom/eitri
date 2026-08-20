package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
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
	m = submitAndWait(t, m)

	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	if len(turnPrompts) != 1 || turnPrompts[0] != "improve this" {
		t.Errorf("args turn seam called with %v, want [\"improve this\"]", turnPrompts)
	}
	content := ansiStrip(view(m))
	n := strings.Index(content, "skill-note-payload")
	a := strings.Index(content, "improve this")
	if n < 0 {
		t.Fatalf("skill note should render, got: %q", content)
	}
	if a < 0 {
		t.Fatalf("args message should render, got: %q", content)
	}
	if n > a {
		t.Errorf("skill note index %d must precede args index %d (note renders before args turn)", n, a)
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
	m = submitAndWait(t, m)

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
	if len(turnPrompts) != 0 {
		t.Errorf("no-args skill must not dispatch a turn, got %v", turnPrompts)
	}
	if !strings.Contains(view(m), "payload-note") {
		t.Errorf("skill payload should render, got: %q", view(m))
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
