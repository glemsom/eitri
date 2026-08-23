package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The seam tests below hit the Splash module directly instead of driving a
// full Model: the module owns the whole launch-splash lifecycle (tick
// advance, keypress skip, early end when the transcript gains content, and
// the title/cursor side-effects), and the Model only routes messages to it
// and drops its pointer on end.

// seamSplash builds a splash module the way NewModelCfg wires one, reusing
// the splash-enabled model harness for its dependencies and transcript.
func seamSplash(t *testing.T) *Splash {
	t.Helper()
	m := splashTitleModel(t, &bytes.Buffer{}, "")
	s := newSplash(m.deps, m.tx, m.kittyGraphics())
	if s == nil {
		t.Fatalf("splash-enabled dependencies must build an active splash")
	}
	return s
}

func TestSplash_handleTickAdvances(t *testing.T) {
	s := seamSplash(t)
	res := s.Handle(splashTickMsg{})
	if !res.handled {
		t.Fatal("an animation tick must be consumed by the splash")
	}
	if res.ended {
		t.Fatal("a tick before the last frame must not end the splash")
	}
	if res.cmd == nil {
		t.Fatal("an advancing tick must schedule the next frame")
	}
	if s.state == nil {
		t.Fatal("splash state must survive an advancing tick")
	}
	if s.state.frame != 1 {
		t.Fatalf("frame after the first tick = %d, want 1", s.state.frame)
	}
}

func TestSplash_handleTickEndsOnLastFrame(t *testing.T) {
	var buf bytes.Buffer
	m := splashTitleModel(t, &buf, "old title")
	s := m.splash
	for s.state.frame < splashTotalFrames-1 {
		res := s.Handle(splashTickMsg{})
		if !res.handled || res.ended {
			t.Fatalf("pre-final ticks must advance, got handled=%v ended=%v", res.handled, res.ended)
		}
	}
	res := s.Handle(splashTickMsg{})
	if !res.handled || !res.ended {
		t.Fatal("the tick onto the last frame must end the splash")
	}
	if s.state != nil {
		t.Fatal("state must clear when the splash ends")
	}
	runCmd(t, res.cmd)
	if !bytes.Contains(buf.Bytes(), []byte(splashCursorShow)) {
		t.Error("splash completion must restore the cursor")
	}
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]0;old title\x07")) {
		t.Error("splash completion must restore the pre-splash title")
	}
}

func TestSplash_handleKeypressSkips(t *testing.T) {
	var buf bytes.Buffer
	m := splashTitleModel(t, &buf, "old title")
	s := m.splash
	res := s.Handle(tea.KeyPressMsg{Code: 'x'})
	if !res.handled || !res.ended {
		t.Fatal("any keypress must consume the splash and end it")
	}
	if s.state != nil {
		t.Fatal("state must clear on a skip keypress")
	}
	runCmd(t, res.cmd)
	if !bytes.Contains(buf.Bytes(), []byte(splashCursorShow)) {
		t.Error("skip must re-show the blinking cursor")
	}
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]0;old title\x07")) {
		t.Error("skip must restore the pre-splash title")
	}
}

func TestSplash_handleIgnoresUnrelatedMessages(t *testing.T) {
	s := seamSplash(t)
	res := s.Handle(tea.WindowSizeMsg{Width: 80, Height: 24})
	if res.handled || res.ended || res.cmd != nil {
		t.Fatal("unrelated messages must pass through the splash untouched")
	}
	if s.state == nil {
		t.Fatal("an unrelated message must not end the splash")
	}
}

func TestSplash_handleTickEndsWhenTranscriptHasContent(t *testing.T) {
	s := seamSplash(t)
	s.tx.appendMsg("help card")
	res := s.Handle(splashTickMsg{})
	if !res.handled || !res.ended {
		t.Fatal("a tick against a content-bearing transcript must end the splash early")
	}
	if s.state != nil {
		t.Fatal("state must clear on the transcript-content early end")
	}
}

func TestSplash_viewRendersFrame(t *testing.T) {
	s := seamSplash(t)
	out := s.View(80, 24)
	if !strings.Contains(out, "\x1b[38;5;") {
		t.Error("splash view must carry ANSI colors")
	}
	if strings.Contains(stripANSI(out), "your terminal coding agent") {
		t.Error("the splash frame must render the animation, not the idle welcome")
	}
}
