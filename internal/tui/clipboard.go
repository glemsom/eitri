package tui

import (
	"io"
	"os"
	"strings"

	"github.com/atotto/clipboard"

	"github.com/glemsom/eitri/internal/osc52"
)

// newClipboard returns the clipboard write seam: the injected Dependencies.Clipboard when set, else the atotto/clipboard package default so Ctrl+O and /copy work out of the box.
func newClipboard(d Dependencies) func(text string) error {
	var primary func(text string) error
	if d.Clipboard != nil {
		primary = d.Clipboard
	} else {
		primary = clipboard.WriteAll
	}
	out := d.OSC52Out
	if out == nil {
		out = os.Stdout
	}
	return clipboardWithOSCFallback(primary, out)
}

// clipboardWithOSCFallback wraps a primary clipboard seam in the OSC 52 fallback: when the primary path fails — e.g. the atotto/clipboard package reporting "Unsupported platform" on a machine without xclip or wl-clipboard — the copy re-routes through the OSC 52 terminal-clipboard writer to the terminal output, which Ghostty and other OSC 52 terminals turn into a system-clipboard write.
func clipboardWithOSCFallback(primary func(text string) error, out io.Writer) func(text string) error {
	return func(text string) error {
		if err := primary(text); err == nil {
			return nil
		}
		return osc52.New(out).Write(text)
	}
}

// copyTranscript copies the plain-text transcript to the system clipboard through the injected seam: Ctrl+O and /copy both route here.
func (m *Model) copyTranscript() {
	if m.clipboard == nil {
		m.savedMsg = "copy failed: clipboard unavailable"
		return
	}
	if err := m.clipboard(m.transcriptText()); err != nil {
		m.savedMsg = "copy failed: " + err.Error()
		return
	}
	m.savedMsg = "copied"
}

// transcriptText renders the conversation log as plain text for clipboard copy : role-marked user prompts and assistant answers, per-turn reasoning blocks, and the interleaved tool-call entries (compact one-liner plus full result when complete) — all ANSI-free so the pasted session is clean.
func (m Model) transcriptText() string {
	var b strings.Builder
	for i, msg := range m.tx.messages {
		if msg.role != "you" && msg.thinkingRequested && msg.reasoning != "" {
			b.WriteString("🤔 " + msg.reasoning + "\n")
		}
		if msg.role == "you" {
			b.WriteString("you: " + msg.content + "\n")
		} else {
			b.WriteString("eitri: " + msg.content + "\n")
		}
		b.WriteString(clipboardToolText(m.tx.log, i))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// clipboardToolText renders every entry anchored to the given message as plain text for the clipboard transcript: the tool head plus the indented full result when complete. It reads the log through its data accessors; the log itself never renders.
func clipboardToolText(l toolLog, anchor int) string {
	var b strings.Builder
	for _, idx := range l.anchoredIndices(anchor) {
		te := l.Entry(idx)
		b.WriteString(toolEntryHead(te))
		b.WriteString("\n")
		if te.complete && te.result != "" {
			b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(te.result, "\n"), "\n", "\n  ") + "\n")
		}
	}
	return b.String()
}
