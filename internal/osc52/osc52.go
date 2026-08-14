// Package osc52 implements the OSC 52 terminal clipboard protocol (issue
// #200): it turns plain text into the escape sequence
// `ESC ] 52 ; c ; <base64> BEL` and writes it to an output writer. Terminals
// like Ghostty, kitty, iTerm2, WezTerm, and foot honour the sequence natively
// and put the payload on the system clipboard, so no external clipboard tool
// is needed. The package is pure Go with no cgo or external binaries.
package osc52

import (
	"encoding/base64"
	"errors"
	"io"

	"golang.org/x/term"
)

// ErrNotTerminal reports that the writer is not attached to a terminal, so no
// escape sequence was emitted: writing OSC 52 into a pipe or file would leak
// escape garbage into non-terminal output (issue #200 AC3).
var ErrNotTerminal = errors.New("osc52: not a terminal")

// fdWriter is the capability an output must expose for the terminal check to
// run: real terminal-backed outputs (*os.File, e.g. os.Stdout) report their
// descriptor; plain buffers and custom writers do not.
type fdWriter interface {
	io.Writer
	Fd() uintptr
}

// Writer turns plain text into an OSC 52 clipboard sequence and writes it to
// an underlying output writer.
type Writer struct {
	w io.Writer
}

// New returns an OSC 52 writer that writes to w.
func New(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write encodes text as base64 and writes the OSC 52 clipboard sequence
// (`ESC ] 52 ; c ; <base64> BEL`) to the underlying writer. It refuses to
// write when the underlying writer is terminal-backed but not attached to a
// terminal (see ErrNotTerminal); writers that expose no Fd (buffers, custom
// writers) are written to directly, which is also what lets tests verify the
// emitted bytes with no real terminal.
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
