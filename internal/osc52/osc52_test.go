package osc52

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestWriteEmitsOSC52Sequence(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := New(&buf).Write("hello"); err != nil {
		t.Fatalf("Write(hello) error = %v, want nil", err)
	}
	want := "\x1b]52;c;aGVsbG8=\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestWriteEncodesUTF8Bytes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := New(&buf).Write("héllo"); err != nil {
		t.Fatalf("Write(héllo) error = %v, want nil", err)
	}
	want := "\x1b]52;c;aMOpbGxv\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

func TestWriteEmptyText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := New(&buf).Write(""); err != nil {
		t.Fatalf("Write(empty) error = %v, want nil", err)
	}
	want := "\x1b]52;c;\x07"
	if got := buf.String(); got != want {
		t.Errorf("buffer = %q, want %q", got, want)
	}
}

type fdBuf struct {
	bytes.Buffer
}

func (*fdBuf) Fd() uintptr { return 12345 }

func TestWriteErrorsWhenNotTerminal(t *testing.T) {
	t.Parallel()
	var w fdBuf
	err := New(&w).Write("hello")
	if !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("Write error = %v, want ErrNotTerminal", err)
	}
	if w.Len() != 0 {
		t.Errorf("non-terminal writer emitted %q, want no garbage", w.String())
	}
}

type shortWriter struct {
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		return w.max, nil
	}
	return len(p), nil
}

func TestWriteSurfacesShortWrite(t *testing.T) {
	t.Parallel()
	w := &shortWriter{max: 3}
	err := New(w).Write("hello")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write error = %v, want io.ErrShortWrite", err)
	}
}
