package osc52

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// Plain text encodes to base64 and lands on the writer wrapped in the OSC 52
// clipboard sequence (ESC ] 52 ; c ; <base64> BEL). The expected bytes are a
// hand-computed literal — base64("hello") is "aGVsbG8=" — independent of the
// encoder's internals (tdd: independent source of truth).
func TestWriteEmitsOSC52Sequence(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf).Write("hello"); err != nil {
		t.Fatalf("Write(hello) error = %v, want nil", err)
	}
	want := "\x1b]52;c;aGVsbG8=\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

// Multi-byte text base64-encodes its UTF-8 bytes, not its runes: "héllo" is
// five runes but six UTF-8 bytes, so the sequence carries the byte encoding.
func TestWriteEncodesUTF8Bytes(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf).Write("héllo"); err != nil {
		t.Fatalf("Write(héllo) error = %v, want nil", err)
	}
	// base64("h\xc3\xa9llo") = "aMOpbGxv"
	want := "\x1b]52;c;aMOpbGxv\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

// Empty text still produces a well-formed sequence with an empty payload, so
// the clipboard is cleared (or set to nothing) rather than emitting a bare
// prefix that a terminal would treat as garbage.
func TestWriteEmptyText(t *testing.T) {
	var buf bytes.Buffer
	if err := New(&buf).Write(""); err != nil {
		t.Fatalf("Write(empty) error = %v, want nil", err)
	}
	want := "\x1b]52;c;\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

// fdBuf is a writer that exposes an Fd like a real terminal-backed file but
// is not a terminal, standing in for stdout piped to a file or another
// process (tdd: the terminal check must not need a real terminal).
type fdBuf struct {
	bytes.Buffer
}

func (*fdBuf) Fd() uintptr { return 12345 }

// A terminal-backed writer that is not a TTY returns a clean error and emits
// nothing: OSC 52 escape bytes must never leak into a pipe or file (issue
// #200 AC3).
func TestWriteErrorsWhenNotTerminal(t *testing.T) {
	var w fdBuf
	err := New(&w).Write("hello")
	if !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("Write error = %v, want ErrNotTerminal", err)
	}
	if w.Len() != 0 {
		t.Errorf("non-terminal writer emitted %q, want no garbage", w.String())
	}
}

// shortWriter accepts at most max bytes per Write and reports the count, the
// io.Writer contract's short-write case.
type shortWriter struct {
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		return w.max, nil
	}
	return len(p), nil
}

// A writer that accepts fewer bytes than the sequence returns io.ErrShortWrite
// instead of reporting success for a truncated OSC 52 sequence (issue #200
// AC1: the full sequence must reach the terminal).
func TestWriteSurfacesShortWrite(t *testing.T) {
	w := &shortWriter{max: 3}
	err := New(w).Write("hello")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write error = %v, want io.ErrShortWrite", err)
	}
}
