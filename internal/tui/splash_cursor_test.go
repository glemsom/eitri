package tui

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The splash owns the whole screen while it plays, so it must hide the hardware
// cursor at start and restore it with blinking enabled when it settles back
// into idleWelcome — including on early exit paths (skip keypress, transcript
// content arriving mid-splash).

func TestSplashCursor_hideEmitsDECTCEMOff(t *testing.T) {
	var buf bytes.Buffer
	runCmd(t, splashStartCmd(&buf))
	if !bytes.Contains(buf.Bytes(), []byte(splashCursorHide)) {
		t.Errorf("splash start must emit cursor-hide %q, got %q", splashCursorHide, buf.String())
	}
}

func TestSplashCursor_showEmitsDECTCEMOnPlusBlink(t *testing.T) {
	var buf bytes.Buffer
	runCmd(t, splashEndCmd(&buf, ""))
	if !bytes.Contains(buf.Bytes(), []byte(splashCursorShow)) {
		t.Errorf("splash end must emit cursor-show + blink-on %q, got %q", splashCursorShow, buf.String())
	}
}

func TestSplashCursor_InitHidesCursor(t *testing.T) {
	var buf bytes.Buffer
	m := splashTitleModel(t, &buf, "")
	// Init batches its startup commands (tea.Batch); running the batch command
	// yields the child commands, so execute them directly before asserting.
	cmd := m.Init()
	if cmd != nil {
		for _, c := range any(cmd()).(tea.BatchMsg) {
			runCmd(t, c)
		}
	}
	if !bytes.Contains(buf.Bytes(), []byte(splashCursorHide)) {
		t.Errorf("Init with active splash must emit cursor-hide \\x1b[?25l, got %q", buf.String())
	}
}

func TestSplashCursor_restoredOnSkip(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\x1b[?25l") // hidden at splash start
	m := splashTitleModel(t, &buf, "old title")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'x'})
	runCmd(t, cmd)
	_ = nm
	if !bytes.Contains(buf.Bytes(), []byte("\x1b[?25h\x1b[?12h")) {
		t.Errorf("splash skip must restore cursor show+blink \\x1b[?25h\\x1b[?12h, got %q", buf.String())
	}
}

func TestSplashCursor_restoredWhenSplashCompletes(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\x1b[?25l")
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
	if !bytes.Contains(buf.Bytes(), []byte("\x1b[?25h\x1b[?12h")) {
		t.Errorf("splash completion must restore cursor show+blink, got %q", buf.String())
	}
}

