package tui

import (
	"bytes"
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// splashTitleModel builds a splash-enabled model whose title escape sequences
// land in buf and whose (fake) pre-existing terminal title is prev.
func splashTitleModel(t *testing.T, buf *bytes.Buffer, prev string) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Splash:        true,
		TitleOut:      buf,
		TerminalTitle: func() string { return prev },
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer"}, nil
		},
	})
	if m.splash == nil {
		t.Fatalf("splash-enabled model must start with an active splash")
	}
	return m
}

// runCmd executes a tea.Cmd and fails the test when it is nil.
func runCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a command, got nil")
	}
	cmd()
}

func TestSplashTitle_beginEmitsBrandingOSC0(t *testing.T) {
	var buf bytes.Buffer
	runCmd(t, splashTitleCmd(&buf, splashWindowTitle))
	want := "\x1b]0;⚒ Eitri — forging agents\x07"
	if buf.String() != want {
		t.Errorf("splash start must emit OSC 0 branding %q, got %q", want, buf.String())
	}
}

func TestSplashTitle_restoresPreviousTitleOnSkip(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\x1b]0;⚒ Eitri — forging agents\x07") // branding emitted at splash start
	m := splashTitleModel(t, &buf, "old title")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'x'})
	runCmd(t, cmd)
	if got := buf.String(); got != "\x1b]0;⚒ Eitri — forging agents\x07\x1b]0;old title\x07\x1b[?25h\x1b[?12h" {
		t.Errorf("splash skip must restore the stored previous title via OSC 0 plus cursor show, got %q", got)
	}
	if am := asModel(t, nm); am.prevTitle != "old title" {
		t.Errorf("previous title must be stored at splash start, got %q", am.prevTitle)
	}
}

func TestSplashTitle_restoresPreviousTitleWhenSplashCompletes(t *testing.T) {
	var buf bytes.Buffer
	m := splashTitleModel(t, &buf, "old title")
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = asModel(t, nm)
	for m.splash != nil && !m.splash.done() {
		nm, cmd := m.Update(splashTickMsg{})
		if cmd != nil {
			cmd()
		}
		m = asModel(t, nm)
	}
	if got := buf.String(); !bytes.Contains(buf.Bytes(), []byte("\x1b]0;old title\x07")) {
		t.Errorf("splash completion must restore the previous title via OSC 0, got %q", got)
	}
}
