package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The cheap live renderers replace the full glamour+goldmark pipeline for the
// body of a *streaming* pane. A live block re-renders its tail on every delta
// and glamour parses the whole markdown each time (~7ms an 8KiB window); the
// cheap paths cost tens of microseconds instead.
//
// Two streaming bodies, deliberately different:
//   - Live reasoning is a "background thought": renderLiveThoughtBody emits the
//     text verbatim with no SGR at all, so the reasoning pane's own italic survives.
//     (Styling the body here did not work out — an embedded emphasis run's
//     `\x1b[0m` reset the pane style mid-line.)
//   - The live answer keeps renderCheapLiveBody, a simplified inline-emphasis
//     pass plus one ANSI word-wrap, so the answer still reads as the answer.
//
// Committed, error, and stopped panes are never routed here (see
// renderPaneBodyFresh), so committed output stays byte-identical to glamour.

var (
	reCheapImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reCheapLink  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reCheapCode  = regexp.MustCompile("`([^`\n]+)`")
	reCheapBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reCheapBoldU = regexp.MustCompile(`__([^_]+)__`)
	reCheapItal  = regexp.MustCompile(`\*([^*\n]+)\*`)
	reCheapItalU = regexp.MustCompile(`_([^_\n]+)_`)
)

// renderCheapLiveBody renders a streaming *answer* body with the cheap ANSI
// word-wrap. It preserves only the simplest inline emphasis (bold, italic,
// inline code, link labels) and word-wraps at the pane content width, falling
// back to a hard break only for a single token longer than the width, so a long
// unbroken token (URL, code run) still cannot produce an overlarge line. The
// output is trimmed of leading and trailing newlines to match the glamour path's
// pane body.
func renderCheapLiveBody(text string, width int) string {
	return strings.Trim(ansi.Wrap(simplifyMarkdownEmphasis(text), width, ""), "\n")
}

// renderLiveThoughtBody renders a *streaming* reasoning body as plain text: no
// markdown parsing and no SGR of any kind. Streaming reasoning is presented as a
// "background thought" by the reasoning pane, so the body must stay style-free —
// any SGR it carried would reset the pane's italic mid-line. The raw text still
// passes through, including any markdown syntax, which parses properly only once
// the turn commits and the block re-renders through glamour. A word-aware ANSI
// wrap at the pane content width, with a hard-break fallback for an over-long
// unbroken token, keeps lines bounded. Leading and trailing newlines are trimmed
// to match the glamour pane body's byte shape.
func renderLiveThoughtBody(text string, width int) string {
	return strings.Trim(ansi.Wrap(text, width, ""), "\n")
}

// simplifyMarkdownEmphasis strips fenced code and link/image syntax, and turns
// the common inline emphasis markers into real SGR so a streaming answer reads
// naturally without paying for a full markdown parse. Everything else (headings,
// lists) is passed through literally — it is the *simplified* emphasis the live
// answer shows while streaming.
func simplifyMarkdownEmphasis(s string) string {
	var b strings.Builder
	inFence := false
	for _, ln := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence // the fence marker itself is dropped
			continue
		}
		if inFence {
			b.WriteString(ln) // fenced code renders verbatim
			b.WriteByte('\n')
			continue
		}
		b.WriteString(simplifyInline(ln))
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// simplifyInline applies the simplified inline emphasis to a single (non-fence)
// line. Order matters: code is handled before bold/italic so a “ `**x**` “
// span is kept whole, and bold before italic so `**x**` is not swallowed by the
// single-asterisk rule.
func simplifyInline(s string) string {
	s = reCheapImage.ReplaceAllString(s, "")
	s = reCheapLink.ReplaceAllString(s, "$1")
	s = reCheapCode.ReplaceAllString(s, "\x1b[1m$1\x1b[0m")
	s = reCheapBoldU.ReplaceAllString(s, "\x1b[1m$1\x1b[0m")
	s = reCheapItalU.ReplaceAllString(s, "\x1b[3m$1\x1b[0m")
	s = reCheapBold.ReplaceAllString(s, "\x1b[1m$1\x1b[0m")
	s = reCheapItal.ReplaceAllString(s, "\x1b[3m$1\x1b[0m")
	return s
}
