package tui

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

func TestPromptHistoryCap(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(3)
	h.Push("one")
	h.Push("two")
	h.Push("three")
	h.Push("four")
	got := h.Entries()
	want := []string{"two", "three", "four"}
	if !equalStrings(got, want) {
		t.Fatalf("entries = %v, want %v (oldest must be dropped at cap)", got, want)
	}
	if h.Len() != 3 {
		t.Fatalf("Len = %d, want 3", h.Len())
	}
}

func TestPromptHistoryConsecutiveDuplicateStoredOnce(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(10)
	h.Push("same")
	h.Push("same")
	h.Push("other")
	h.Push("same")
	got := h.Entries()
	want := []string{"same", "other", "same"}
	if !equalStrings(got, want) {
		t.Fatalf("entries = %v, want %v (consecutive duplicates collapse, non-consecutive repeat kept)", got, want)
	}
}

func TestPromptHistoryNewAllowsPreviouslyDroppedDuplicate(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(10)
	h.Push("a")
	h.Push("b")
	h.Push("a")
	if got := h.Entries(); !equalStrings(got, []string{"a", "b", "a"}) {
		t.Fatalf("entries = %v, want [a b a]", got)
	}
}

func TestPromptHistoryEmptyIgnored(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(10)
	h.Push("")
	h.Push("   ")
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0 (empty prompts must never be recorded)", h.Len())
	}
}

func TestPromptHistoryZeroCapacity(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(0)
	h.Push("anything")
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0", h.Len())
	}
}

func TestPromptHistoryEntriesIsCopy(t *testing.T) {
	t.Parallel()
	h := NewPromptHistory(10)
	h.Push("x")
	got := h.Entries()
	got[0] = "mutated"
	if h.Entries()[0] != "x" {
		t.Fatal("Entries mutated the underlying ring")
	}
}

func TestModelSubmitRecordsUserPrompt(t *testing.T) {
	var prompted string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "first prompt")
	m = submitAndWait(t, m)
	if prompted != "first prompt" {
		t.Fatalf("turn prompt = %q, want first prompt", prompted)
	}
	if got := m.history.Entries(); !equalStrings(got, []string{"first prompt"}) {
		t.Fatalf("history = %v, want [first prompt]", got)
	}

	m = typeText(t, m, "second prompt")
	m = submitAndWait(t, m)
	if got := m.history.Entries(); !equalStrings(got, []string{"first prompt", "second prompt"}) {
		t.Fatalf("history = %v, want both prompts in submission order", got)
	}
}

func TestModelSubmitRecordsSkillActivation(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items:    []SkillItem{{Name: "review"}},
			Activate: func(context.Context, string) (string, error) { return "payload", nil },
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/review my diff")
	m = submitAndWait(t, m)
	if got := m.history.Entries(); !equalStrings(got, []string{"/review my diff"}) {
		t.Fatalf("history = %v, want the full /skill line recorded", got)
	}
}

func TestModelSubmitExcludesControlSlashCommands(t *testing.T) {
	// Control slash commands must never be recorded; each is submitted on a
	// fresh Model because `/settings` opens an overlay (and `/login` starts a
	// flow) that would otherwise swallow later Enter keys.
	for _, cmd := range []string{"/settings", "/new", "/help", "/copy", "/login"} {
		var prompted string
		m := NewModelCfg(Dependencies{
			Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
				prompted = prompt
				return TurnResult{Answer: "ok"}, nil
			},
			Config: cfgFixture(),
			Login: func(_ context.Context, onCode func(LoginCode)) (config.Config, error) {
				onCode(LoginCode{UserCode: "ZZ-AA", VerificationURI: "https://x"})
				return config.Config{}, nil
			},
		})
		m = resize(t, m)
		m = typeText(t, m, cmd)
		m = keypress(t, m, "enter")
		_ = prompted
		if h := m.history.Entries(); len(h) != 0 {
			t.Fatalf("%s: history = %v, want empty (control slash commands must be excluded)", cmd, h)
		}
	}
}

func TestModelSubmitExcludesEmptyDraft(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "enter") // empty draft: toggles focused block, never a turn or history entry
	if h := m.history.Entries(); len(h) != 0 {
		t.Fatalf("history = %v, want empty", h)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
