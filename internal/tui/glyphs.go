package tui

import (
	"os"
	"strings"
)

// The TUI's decorative glyph charter (benchmark §3.6/§4.3): every non-ASCII glyph the surface renders has an ASCII fallback, selected when the terminal locale cannot render non-ASCII characters (or EITRI_ASCII_GLYPHS=1 forces the fallback, for testing).
func g(utf8, ascii string) string {
	if os.Getenv("EITRI_ASCII_GLYPHS") != "" || !localeSupportsUTF8() {
		return ascii
	}
	return utf8
}

// glyph is one production marker in the surface charter.
type glyph struct {
	utf8  string
	ascii string
	width int
}

// glyphInventory is the single source of truth for every production marker.
// Every entry has an ASCII fallback and a declared, stable cell width on the
// default UTF-8 path. Color comes from theme roles, never from the glyph's
// own presentation.
var glyphInventory = map[string]glyph{
	"toolBash":            {"❯", "$", 1},
	"toolWeb":             {"◎", "W", 1},
	"toolGeneric":         {"⊕", "+", 1},
	"reasoning":           {"≡", "?", 1},
	"brand":               {"⚒", "+", 1},
	"focus":               {"▸", ">", 1},
	"ok":                  {"✓", "ok", 1},
	"fail":                {"✗", "X", 1},
	"stopped":             {"⏹", "!", 1},
	"warning":             {"⚠", "!", 1},
	"hr":                  {"─", "-", 1},
	"keyHint":             {"⌨", "k", 1},
	"categoryComposer":    {"✎", "c", 1},
	"categoryNav":         {"→", "n", 1},
	"categoryPanes":       {"▦", "p", 1},
	"categoryActions":     {"★", "a", 1},
	"settingsModel":       {"⚙", "*", 1},
	"settingsCredentials": {"✱", "*", 1},
	"settingsReasoning":   {"≡", "*", 1},
	"settingsAppearance":  {"◈", "*", 1},
	"settingsWorkspace":   {"◉", "*", 1},
	"unsaved":             {"●", "*", 1},
	"on":                  {"✓", "*", 1},
	"off":                 {"○", "*", 1},
	"palette":             {"██", "##", 2},
	"cursor":              {"┃", "|", 1},
}

// lookup returns the glyph for the current locale. Panics if name is not in the inventory so a typo is caught immediately during development/testing.
func lookup(name string) string {
	ent, ok := glyphInventory[name]
	if !ok {
		panic("unknown glyph: " + name)
	}
	return g(ent.utf8, ent.ascii)
}

// failurePrefix is the error-shaped assistant content prefix ("⚠ "), with its ASCII "! " fallback.
func failurePrefix() string { return lookup("warning") + " " }

// stoppedMarker returns the suffix marking a user-stopped turn's partial output ("⏹ stopped"), with its ASCII "! stopped" fallback. renderHistory appends it under the stopped message's pane so the aborted turn reads as deliberately stopped, never as an error.
func stoppedMarker() string { return lookup("stopped") + " stopped" }

// toolGlyph maps a tool name to its per-tool glyph, with an ASCII fallback.
func toolGlyph(name string) string {
	switch name {
	case "bash":
		return lookup("toolBash")
	case "open_in_browser":
		return lookup("toolWeb")
	}
	return lookup("toolGeneric")
}

// brandMark returns the ⚒ brand glyph with its "+" ASCII fallback.
func brandMark() string { return lookup("brand") }

// focusMarker returns the ▸ cursor glyph prefixing a focused collapsible block's hint/head line, with its ASCII fallback.
func focusMarker() string { return lookup("focus") }

// hr returns a horizontal-rule separator (──) with its "--" ASCII fallback.
func hr() string { return lookup("hr") + lookup("hr") }

// hrWidth returns a horizontal-rule separator repeated to the given width.
func hrWidth(w int) string {
	if w < 1 {
		return ""
	}
	return strings.Repeat(lookup("hr"), w)
}

// keyHint returns the ⌨ glyph for the keybinding hint line.
func keyHint() string { return lookup("keyHint") }

func categoryEmoji(name string) string {
	switch name {
	case "COMPOSER":
		return lookup("categoryComposer")
	case "NAVIGATION":
		return lookup("categoryNav")
	case "PANES":
		return lookup("categoryPanes")
	case "ACTIONS":
		return lookup("categoryActions")
	}
	return ""
}
