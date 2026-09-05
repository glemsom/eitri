package tui

import (
	"bytes"
	"errors"
	"testing"
)

func failingClipboard(string) error {
	return errors.New("Unsupported platform")
}

func TestNewClipboardDefaultsToOSC52(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	clip := newClipboard(Dependencies{OSC52Out: &out})
	if err := clip("hello"); err != nil {
		t.Fatalf("clip(hello) error = %v, want nil", err)
	}
	want := "\x1b]52;c;aGVsbG8=\x07"
	if got := out.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewClipboardUsesExplicitAdapter(t *testing.T) {
	t.Parallel()
	var copied string
	var out bytes.Buffer
	clip := newClipboard(Dependencies{
		Clipboard: func(s string) error { copied = s; return nil },
		OSC52Out:  &out,
	})
	if err := clip("hello"); err != nil {
		t.Fatalf("clip(hello) error = %v, want nil", err)
	}
	if copied != "hello" {
		t.Errorf("explicit adapter received %q, want %q", copied, "hello")
	}
	if out.Len() != 0 {
		t.Errorf("explicit adapter wrote OSC 52 output %q, want none", out.String())
	}
}

func TestNewClipboardReturnsExplicitAdapterFailureWithoutOSC52(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	clip := newClipboard(Dependencies{Clipboard: failingClipboard, OSC52Out: &out})
	if err := clip("hello"); err == nil || err.Error() != "Unsupported platform" {
		t.Fatalf("clip(hello) error = %v, want Unsupported platform", err)
	}
	if out.Len() != 0 {
		t.Errorf("explicit adapter failure wrote OSC 52 output %q, want none", out.String())
	}
}
