package tui

import (
	"os"
)

// The TUI's decorative glyph charter (benchmark §3.6/§4.3): every non-ASCII glyph the surface renders has an ASCII fallback, selected when the terminal locale cannot render non-ASCII characters (or EITRI_ASCII_GLYPHS=1 forces the fallback, for testing).
func g(utf8, ascii string) string {
	if os.Getenv("EITRI_ASCII_GLYPHS") != "" || !localeSupportsUTF8() {
		return ascii
	}
	return utf8
}

// failurePrefix is the error-shaped assistant content prefix ("⚠ "), with its ASCII "! " fallback.
func failurePrefix() string { return g("⚠ ", "! ") }

// stoppedMarker returns the suffix marking a user-stopped turn's partial output ("⏹ stopped"), with its ASCII "! stopped" fallback. renderHistory appends it under the stopped message's pane so the aborted turn reads as deliberately stopped, never as an error.
func stoppedMarker() string { return g("⏹ stopped", "! stopped") }

// toolGlyph maps a tool name to its per-tool emoji glyph, with an ASCII fallback.
func toolGlyph(name string) string {
	switch name {
	case "bash":
		return g("🔧", "$")
	case "open_in_browser":
		return g("🌍", "W")
	}
	return g("⊕", "+")
}

// brandMark returns the ⚒ brand glyph with its "+" ASCII fallback.
func brandMark() string { return g("⚒", "+") }

// focusMarker returns the ▸ cursor glyph prefixing a focused collapsible block's hint/head line, with its ASCII fallback.
func focusMarker() string { return g("▸", ">") }

// hr returns a horizontal-rule separator (──) with its "--" ASCII fallback.
func hr() string { return g("──", "--") }

// keyHint returns the ⌨ glyph for the keybinding hint line.
func keyHint() string { return g("⌨", "k") }

func categoryEmoji(name string) string {
	switch name {
	case "COMPOSER":
		return g("✍️", "c")
	case "NAVIGATION":
		return g("🧭", "n")
	case "PANES":
		return g("▦", "p")
	case "ACTIONS":
		return g("⚡", "a")
	}
	return ""
}
