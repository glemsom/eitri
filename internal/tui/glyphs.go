package tui

import (
	"os"
)

// The TUI's decorative glyph charter (benchmark §3.6/§4.3): every non-ASCII
// glyph the surface renders has an ASCII fallback, selected when the terminal
// locale cannot render non-ASCII characters (or EITRI_ASCII_GLYPHS=1 forces
// the fallback, for testing). Glyphs never carry meaning alone — color,
// position, and text do — so the degradation preserves the surface's
// information while keeping a non-UTF-8 terminal readable instead of a field
// of tofu boxes. The braille spinner frames already fall back to the static
// "… thinking" line through the motion gate.
//
// g returns the UTF-8 glyph, or its ASCII fallback when applicable.
func g(utf8, ascii string) string {
	if os.Getenv("EITRI_ASCII_GLYPHS") != "" || !localeSupportsUTF8() {
		return ascii
	}
	return utf8
}

// failurePrefix is the error-shaped assistant content prefix ("⚠ "), with its
// ASCII "! " fallback. The engine writes it, and the pane-border check in
// renderHistory matches it, so both sides must agree.
func failurePrefix() string { return g("⚠ ", "! ") }

// stoppedMarker returns the suffix marking a user-stopped turn's partial
// output ("⏹ stopped"), with its ASCII "! stopped" fallback. renderHistory
// appends it under the stopped message's pane so the aborted turn reads as
// deliberately stopped, never as an error.
func stoppedMarker() string { return g("⏹ stopped", "! stopped") }
