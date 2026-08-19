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

// toolGlyph maps a tool name to its per-tool emoji glyph, with an ASCII
// fallback. Unknown or future tools return the generic ⊕/+, preserving the
// fallback contract so color alone never carries meaning.
func toolGlyph(name string) string {
	switch name {
	case "bash":
		return g("🔧", "$")
	case "read":
		return g("📖", "<")
	case "write":
		return g("✏️", ">")
	case "edit":
		return g("✂️", "~")
	case "web_fetch":
		return g("🌐", "w")
	case "open_in_browser":
		return g("🌍", "W")
	case "skill":
		return g("⚡", "s")
	}
	return g("⊕", "+")
}

// brandMark returns the ⚒ brand glyph with its "+" ASCII fallback.
func brandMark() string { return g("⚒", "+") }

// hr returns a horizontal-rule separator (──) with its "--" ASCII fallback.
func hr() string { return g("──", "--") }

// promptHint returns the 💬 glyph for the prompt hint line.
func promptHint() string { return g("💬", ">") }

// keyHint returns the ⌨ glyph for the keybinding hint line.
func keyHint() string { return g("⌨", "k") }

// categoryEmoji returns the emoji prefix for a KEYBINDINGS category header
// (issue #386), with its ASCII fallback, so the labeled sub-groups share the
// repository's glyph conventions.
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
