package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func failingClipboard(text string) error {
	return errors.New("Unsupported platform")
}

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

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write error") }

type notTerminal struct {
	bytes.Buffer
}

func (*notTerminal) Fd() uintptr { return 12345 }

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
