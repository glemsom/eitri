package tui

import (
	"strings"
	"testing"
)

// TestRailReflectsMutableSessionKey guards the T1 rail seam: the rail's
// CONTEXT session id must update when the shared mutable session key changes,
// so a later `/new` re-mint is reflected in the on-screen session identity
// without re-wiring the rail closure.
func TestRailReflectsMutableSessionKey(t *testing.T) {
	live := NewLiveSessionKey("eitri-1")
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	r.SetLiveKey(live)

	view := r.render(NewTelemetry("deepseek-v4-flash", "high", true, 250), defaultTheme, defaultRailWidth)
	if !strings.Contains(view, "session eitri-1") {
		t.Fatalf("rail CONTEXT missing initial session id, got: %q", view)
	}

	live.Set("eitri-2") // `/new` re-mints the session key
	view = r.render(NewTelemetry("deepseek-v4-flash", "high", true, 250), defaultTheme, defaultRailWidth)
	if !strings.Contains(view, "session eitri-2") {
		t.Errorf("rail CONTEXT did not refresh to new session id after live key changed, got: %q", view)
	}
	if strings.Contains(view, "session eitri-1") {
		t.Errorf("rail CONTEXT still shows stale session id, got: %q", view)
	}
}
