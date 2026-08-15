package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The args follow-up turn is dispatched through the shared composer submit
// path (startTurn), so submitAndWait — whose runSubmitted helper now threads the
// command returned by Update — drives the whole chain: submit the /skillname
// line, run the activation, then run the follow-up args turn.

// TestModel_slashSkillWithArgs threads `/skillname <args>` through activation:
// the activation seam runs with the bare skill name, the payload renders as an
// assistant note, then the args are dispatched as a normal user turn (the
// injected Turn seam) verbatim, and the args message renders after the note.
func TestModel_slashSkillWithArgs(t *testing.T) {
	var activated string
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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

	// (a) activation seam sees the bare name, NOT "/my-skill improve this".
	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	// (b) args dispatched as a user turn, verbatim.
	if len(turnPrompts) != 1 || turnPrompts[0] != "improve this" {
		t.Errorf("args turn seam called with %v, want [\"improve this\"]", turnPrompts)
	}
	content := ansiStrip(view(m))
	// (c)/(d) skill note appears before the args, and args message renders.
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

// TestModel_slashSkillWithMultiWordArgs asserts multi-word args are delivered
// to the Turn seam verbatim, trimmed of surrounding whitespace.
func TestModel_slashSkillWithMultiWordArgs(t *testing.T) {
	want := "Let us improve this codebase"
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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

// TestModel_slashSkillNoArgs asserts bare `/skillname` activates the skill but
// does NOT dispatch any follow-up turn.
func TestModel_slashSkillNoArgs(t *testing.T) {
	var activated string
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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

// TestModel_slashSkillActivationError asserts an activation failure surfaces
// the existing failure message and dispatches NO follow-up turn.
func TestModel_slashSkillActivationError(t *testing.T) {
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ SkillInject) (TurnResult, error) {
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
	// Words can be split across wrapped lines with SGR runs between them, so
	// assert on words that stay intact rather than one multi-word phrase.
	if !strings.Contains(content, "boom") || !strings.Contains(content, "failed") {
		t.Errorf("activation failure should render, got: %q", content)
	}
}
