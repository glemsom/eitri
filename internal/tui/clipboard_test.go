package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A failing primary clipboard stands in for the atotto/clipboard package on a
// machine without xclip/wl-clipboard: its WriteAll error is what triggers the
// OSC 52 fallback.
func failingClipboard(text string) error {
	return errors.New("Unsupported platform")
}

// When the primary clipboard path fails, the seam falls back to the OSC 52
// terminal-clipboard sequence on the captured output writer — the exact bytes
// a terminal like Ghostty turns into a system-clipboard write, verified against
// a hand-computed literal.
func TestClipboardWithOSCFallbackFallsBackToOSC52(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	seam := clipboardWithOSCFallback(failingClipboard, &out)
	if err := seam("hello"); err != nil {
		t.Fatalf("seam(hello) error = %v, want nil", err)
	}
	want := "\x1b]52;c;aGVsbG8=\x07"
	if got := out.String(); got != want {
		t.Errorf("fallback output = %q, want %q", got, want)
	}
}

// A successful primary copy short-circuits the chain: the text reaches the
// clipboard and no OSC 52 sequence is ever written, so a working system
// clipboard keeps its exact pre-fallback behaviour.
func TestClipboardWithOSCFallbackPrimarySuccessSkipsFallback(t *testing.T) {
	t.Parallel()
	var copied string
	primary := func(text string) error { copied = text; return nil }
	var out bytes.Buffer
	seam := clipboardWithOSCFallback(primary, &out)
	if err := seam("hello"); err != nil {
		t.Fatalf("seam(hello) error = %v, want nil", err)
	}
	if copied != "hello" {
		t.Errorf("primary received %q, want %q", copied, "hello")
	}
	if out.Len() != 0 {
		t.Errorf("fallback wrote %q, want no OSC 52 on primary success", out.String())
	}
}

// errWriter fails every write, standing in for a terminal output that cannot
// accept the sequence (e.g. a broken pipe).
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write error") }

// notTerminal is a writer that exposes an Fd like a real terminal-backed file
// but is not a terminal, standing in for stdout piped to a file or another
// process: the OSC 52 writer's own guard refuses it.
type notTerminal struct {
	bytes.Buffer
}

func (*notTerminal) Fd() uintptr { return 12345 }

// When the primary path and the OSC 52 fallback both fail, the chain surfaces
// the fallback error so the existing "copy failed: …" status note still
// reports the failure instead of claiming a copy that never happened.
func TestClipboardWithOSCFallbackBothFail(t *testing.T) {
	t.Parallel()
	seam := clipboardWithOSCFallback(failingClipboard, errWriter{})
	err := seam("hello")
	if err == nil {
		t.Fatal("seam(hello) error = nil, want the fallback write error")
	}
	if !strings.Contains(err.Error(), "write error") {
		t.Errorf("error = %v, want it to carry the fallback write error", err)
	}
}

// A non-terminal fallback output is refused by the OSC 52 writer's own guard
// the seam returns osc52.ErrNotTerminal and emits nothing, so piped/
// non-terminal output never receives escape garbage.
func TestClipboardWithOSCFallbackRefusesNonTerminalOutput(t *testing.T) {
	t.Parallel()
	var out notTerminal
	seam := clipboardWithOSCFallback(failingClipboard, &out)
	err := seam("hello")
	if err == nil {
		t.Fatal("seam(hello) error = nil, want a non-terminal error")
	}
	if out.Len() != 0 {
		t.Errorf("non-terminal fallback emitted %q, want no escape garbage", out.String())
	}
}
