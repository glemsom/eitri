package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// TestModel_unknownThemeStartupWarning asserts a config holding an unknown
// theme value surfaces a one-time status-strip warning on startup naming the
// fallback (issue #131 AC1): the very first rendered frame warns "unknown theme
// \"bogus\", using dark", and the warning never repeats on later frames so a
// long-lived session isn't spammed. The first frame is the initial View the
// Bubble Tea runtime renders before any message is processed, so the test
// checks it before feeding a resize.
func TestModel_unknownThemeStartupWarning(t *testing.T) {
	cfg := cfgFixture()
	cfg.Theme = "bogus"
	m := NewModelCfg(Dependencies{Config: cfg})

	first := m.View()
	if !strings.Contains(first, "unknown theme") {
		t.Errorf("first view missing unknown-theme warning, got: %q", first)
	}
	if !strings.Contains(first, `"bogus"`) {
		t.Errorf("first view missing the offending theme value, got: %q", first)
	}
	if !strings.Contains(first, config.DefaultTheme) {
		t.Errorf("first view missing the fallback theme name, got: %q", first)
	}

	// The warning is one-time: once any message lands (here a resize), it must
	// not appear on the next frame.
	m = resize(t, m)
	if second := m.View(); strings.Contains(second, "unknown theme") {
		t.Errorf("warning repeated on second frame, got: %q", second)
	}
}

// TestModel_validThemeNoWarning asserts a supported theme never triggers the
// unknown-theme startup warning (issue #131 AC1): valid themes print nothing.
func TestModel_validThemeNoWarning(t *testing.T) {
	cfg := cfgFixture()
	cfg.Theme = "dracula"
	m := NewModelCfg(Dependencies{Config: cfg})
	m = resize(t, m)

	if view := m.View(); strings.Contains(view, "unknown theme") {
		t.Errorf("valid theme must not warn, got: %q", view)
	}
}

// TestModel_statusNoteIsOneShot asserts a status note set during an Update
// (here the copy note, issue #123) renders on the next frame and is gone on the
// one after: the band note must not repeat forever (issue #131 AC1 hardening —
// the same one-shot discipline the startup warning relies on).
func TestModel_statusNoteIsOneShot(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m) // sets the "copied" note

	if view := m.View(); !strings.Contains(view, "copied") {
		t.Fatalf("expected copy note on the frame after Ctrl+O, got: %q", view)
	}
	// A follow-up event (another resize) clears the note.
	m = resize(t, m)
	if view := m.View(); strings.Contains(view, "copied") {
		t.Errorf("copy note repeated after a later update, got: %q", view)
	}
}
