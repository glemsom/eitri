package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The cheap live renderer (scratch issue 02) replaces the full
// glamour+goldmark pipeline for the body of a *streaming* reasoning or answer
// pane. A live block re-renders its tail on every delta and glamour parses the
// whole markdown each time (~7ms an 8KiB window); the cheap path does a single
// simplified inline-emphasis pass and one ANSI hard-wrap, so each delta lands
// in tens of microseconds. Committed, error, and stopped panes are never
// routed here (see renderPaneBodyFresh), so committed output stays byte-
// identical to glamour.

var (
	reCheapImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reCheapLink  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reCheapCode  = regexp.MustCompile("`([^`\n]+)`")
	reCheapBold  = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reCheapBoldU = regexp.MustCompile(`__([^_]+)__`)
	reCheapItal  = regexp.MustCompile(`\*([^*\n]+)\*`)
	reCheapItalU = regexp.MustCompile(`_([^_\n]+)_`)
)

// renderCheapLiveBody renders a streaming reasoning/answer body with the cheap
// ANSI word-wrap. It preserves only the simplest inline emphasis (bold, italic,
// inline code, link labels) and hard-wraps at the pane content width so a long
// unbroken token (URL, code run) still cannot produce an overlarge line. The
// output is trimmed of trailing newlines to match the glamour path's pane body.
func renderCheapLiveBody(text string, width int) string {
	return strings.TrimRight(ansi.Hardwrap(simplifyMarkdownEmphasis(text), width, false), "\n")
}

// simplifyMarkdownEmphasis strips fenced code and link/image syntax, and turns
// the common inline emphasis markers into real SGR so a streaming body reads
// naturally without paying for a full markdown parse. Everything else (headings,
// lists) is passed through literally — it is the *simplified* emphasis the live
// body shows while streaming.
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
