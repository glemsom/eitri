package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, cmd := range []string{"/settings", "/new", "/help", "/login"} {
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

func TestNewPersistedPromptHistoryLoads(t *testing.T) {
	dir := t.TempDir()
	path := PromptHistoryPath(dir)
	if err := os.WriteFile(path, []byte(`["one","two","three"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewPersistedPromptHistory(10, path)
	if got, want := h.Entries(), []string{"one", "two", "three"}; !equalStrings(got, want) {
		t.Fatalf("restored entries = %v, want %v", got, want)
	}
}

func TestNewPersistedPromptHistoryMissingFileEmpty(t *testing.T) {
	h := NewPersistedPromptHistory(10, filepath.Join(t.TempDir(), "prompt_history.json"))
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0 for a missing file", h.Len())
	}
}

func TestNewPersistedPromptHistoryCorruptFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewPersistedPromptHistory(10, path)
	if h.Len() != 0 {
		t.Fatalf("Len = %d, want 0 (corrupt file must fall back to empty)", h.Len())
	}
}

func TestNewPersistedPromptHistoryTruncatesToCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	// Entries beyond the ring capacity must be dropped (oldest first), keeping
	// the most recent prompts.
	if err := os.WriteFile(path, []byte(`["a","b","c"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewPersistedPromptHistory(2, path)
	if got, want := h.Entries(), []string{"b", "c"}; !equalStrings(got, want) {
		t.Fatalf("entries = %v, want %v (capacity applied on restore)", got, want)
	}
}

func TestPersistedPushWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	h := NewPersistedPromptHistory(10, path)
	h.Push("hello")
	h.Push("world")
	got := readPromptHistoryFile(t, path)
	if want := []string{"hello", "world"}; !equalStrings(got, want) {
		t.Fatalf("persisted = %v, want %v", got, want)
	}
}

func TestPersistedHistorySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	h := NewPersistedPromptHistory(10, path)
	h.Push("first")
	h.Push("second")

	reopened := NewPersistedPromptHistory(10, path)
	if got, want := reopened.Entries(), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("reopened entries = %v, want %v (history must survive a restart)", got, want)
	}
}

func readPromptHistoryFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read history file: %v", err)
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal history file: %v", err)
	}
	return entries
}

func TestModelPersistsHistoryAcrossNewModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt_history.json")
	newModel := func() Model {
		m := NewModelCfg(Dependencies{
			Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
				return TurnResult{Answer: "ok"}, nil
			},
			Config:      cfgFixture(),
			HistoryPath: path,
		})
		m = resize(t, m)
		return m
	}
	m := newModel()
	m = typeText(t, m, "persisted prompt")
	m = submitAndWait(t, m)

	reopened := newModel()
	if got := reopened.history.Entries(); !equalStrings(got, []string{"persisted prompt"}) {
		t.Fatalf("reopened history = %v, want [persisted prompt] (must survive a restart)", got)
	}
}
