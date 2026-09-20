package tui

import (
	"context"
	"strings"
	"testing"
)

func TestRoleMarks_userPromptCarriesRoleMark(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	plain := plain(view(m))
	if !strings.Contains(plain, userRoleMark()) {
		t.Errorf("user prompt must carry the user role mark %q, got:\n%s", userRoleMark(), plain)
	}
}

func TestRoleMarks_assistantTurnOmitsRoleMark(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	plain := plain(view(m))
	if strings.Contains(plain, assistantRoleMark()) {
		t.Errorf("assistant response must omit the assistant role mark %q, got:\n%s", assistantRoleMark(), plain)
	}
}

func TestRoleMarks_notInCopyablePayload(t *testing.T) {
	t.Parallel()
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	view(m)

	rows, top := historyContentRows(m)
	if top != 0 {
		t.Fatalf("test assumes offset 0, got %d", top)
	}
	// Select the entire first line of the assistant answer.
	row, rowY := "", 0
	for i, r := range rows {
		if strings.Contains(r, "plain") {
			row, rowY = r, i
			break
		}
	}
	if row == "" {
		t.Fatalf("could not locate the answer row, got: %q", rows)
	}

	m = mustUpdate(t, m, dragMsg("press", 0, rowY))
	m = mustUpdate(t, m, dragMsg("motion", len([]rune(row))-1, rowY))
	mustUpdate(t, m, dragMsg("release", len([]rune(row))-1, rowY))

	if strings.Contains(copied, userRoleMark()) {
		t.Errorf("copied text must not contain user role mark %q, got: %q", userRoleMark(), copied)
	}
	if strings.Contains(copied, assistantRoleMark()) {
		t.Errorf("copied text must not contain assistant role mark %q, got: %q", assistantRoleMark(), copied)
	}
}
