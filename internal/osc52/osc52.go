// Package osc52 implements the OSC 52 terminal clipboard protocol: it turns plain text into the escape sequence `ESC ] 52 ; c ; <base64> BEL` and writes it to an output writer.
package osc52

import (
	"encoding/base64"
	"errors"
	"io"

	"golang.org/x/term"
)

// ErrNotTerminal reports that the writer is not attached to a terminal, so no escape sequence was emitted: writing OSC 52 into a pipe or file would leak escape garbage into non-terminal output.
var ErrNotTerminal = errors.New("osc52: not a terminal")

// fdWriter is the capability an output must expose for the terminal check to run: real terminal-backed outputs (*os.File, e.g. os.Stdout) report their descriptor; plain buffers and custom writers do not.
type fdWriter interface {
	io.Writer
	Fd() uintptr
}

// Writer turns plain text into an OSC 52 clipboard sequence and writes it to an underlying output writer.
type Writer struct {
	w io.Writer
}

// New returns an OSC 52 writer that writes to w.
func New(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write encodes text as base64 and writes the OSC 52 clipboard sequence (`ESC ] 52 ; c ; <base64> BEL`) to the underlying writer.
func (wr *Writer) Write(text string) error {
	if f, ok := wr.w.(fdWriter); ok && !term.IsTerminal(int(f.Fd())) {
		return ErrNotTerminal
	}
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	n, err := io.WriteString(wr.w, seq)
	if err != nil {
		return err
	}
	if n < len(seq) {
		return io.ErrShortWrite
	}
	return nil
}
