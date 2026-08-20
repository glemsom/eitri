package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_ctrlOCopiesTranscript(t *testing.T) {
	t.Parallel()
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "I reason first."}, nil
		},
		Clipboard: func(s string) error { copied = s; return nil },
		Config:    config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	if !strings.Contains(copied, "you: hello") {
		t.Errorf("copied text missing the user prompt, got: %q", copied)
	}
	if !strings.Contains(copied, "eitri: plain answer") {
		t.Errorf("copied text missing the assistant answer, got: %q", copied)
	}
	if !strings.Contains(copied, "I reason first") {
		t.Errorf("copied text missing the reasoning block, got: %q", copied)
	}
	if strings.Contains(copied, "\x1b[") {
		t.Errorf("copied text must be ANSI-free plain text, got: %q", copied)
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

func TestModel_ctrlOHidesReasoningWhenThinkingOff(t *testing.T) {
	t.Parallel()
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "sneaked chain-of-thought"}, nil
		},
		Clipboard: func(s string) error { copied = s; return nil },
		Config:    config.Config{ThinkingEnabled: false},
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	if !strings.Contains(copied, "eitri: plain answer") {
		t.Errorf("copied text missing the assistant answer, got: %q", copied)
	}
	if strings.Contains(copied, "sneaked chain-of-thought") {
		t.Errorf("thinking-off turn must not copy reasoning, got: %q", copied)
	}
}

func TestModel_copySlashCopiesTranscript(t *testing.T) {
	t.Parallel()
	var copied string
	var prompted []string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = append(prompted, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = typeText(t, m, "/copy")
	m = keypress(t, m, "enter")

	if !strings.Contains(copied, "you: hello") {
		t.Errorf("`/copy` should copy the transcript, got: %q", copied)
	}
	for _, p := range prompted {
		if p == "/copy" {
			t.Errorf("`/copy` must not reach the engine seam, got prompt %q", p)
		}
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

func TestModel_copyFailureReportsNote(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return errors.New("no display") },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	if !strings.Contains(view(m), "copy failed") {
		t.Errorf("expected a copy failure note in view, got: %q", view(m))
	}
}

func TestModel_copyFallsBackToOSC52(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Clipboard: func(string) error { return errors.New("no display") },
		OSC52Out:  &out,
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	seq := out.String()
	if !strings.HasPrefix(seq, "\x1b]52;c;") {
		t.Errorf("fallback output must be an OSC 52 sequence, got: %q", seq)
	}
	if !strings.HasSuffix(seq, "\x07") {
		t.Errorf("fallback output must end with the BEL terminator, got: %q", seq)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("fallback payload %q is not valid base64: %v", payload, err)
	}
	if !strings.Contains(string(decoded), "you: hello") {
		t.Errorf("fallback output missing the transcript, got: %q", decoded)
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

func TestModel_copyDoesNotMutateConversation(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)
	before := m.tx.messages

	m = keypressCtrlO(t, m)

	if len(m.tx.messages) != len(before) {
		t.Errorf("copy changed the message count: got %d, want %d", len(m.tx.messages), len(before))
	}
	for i := range before {
		if !reflect.DeepEqual(before[i], m.tx.messages[i]) {
			t.Errorf("message %d changed by copy: got %+v, want %+v", i, m.tx.messages[i], before[i])
		}
	}
	if m.tx.busy {
		t.Errorf("copy must not mark the model busy")
	}
}

func TestModel_copySlashShowsInCompletion(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/")
	if !strings.Contains(view(m), "/copy") {
		t.Errorf("bare `/` completion should list /copy, got: %q", view(m))
	}

	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/settings" {
		t.Fatalf("first tab completion = %q, want /settings", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/copy" {
		t.Errorf("second tab completion = %q, want /copy", got)
	}
}

func keypressCtrlO(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	return asModel(t, nm)
}
