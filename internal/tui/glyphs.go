package tui

import "strings"

// glyph is one production marker in the surface charter.
type glyph struct {
	utf8 string
}

// glyphInventory is the single source of truth for every production marker.
// Color comes from theme roles, never from the glyph's own presentation.
var glyphInventory = map[string]glyph{
	"toolBash":            {"❯"},
	"toolWeb":             {"◎"},
	"toolGeneric":         {"⊕"},
	"reasoning":           {"≡"},
	"brand":               {"⚒"},
	"focus":               {"▸"},
	"ok":                  {"✓"},
	"fail":                {"✗"},
	"stopped":             {"⏹"},
	"warning":             {"⚠"},
	"hr":                  {"─"},
	"keyHint":             {"⌨"},
	"categoryComposer":    {"✎"},
	"categoryNav":         {"→"},
	"categoryPanes":       {"▦"},
	"categoryActions":     {"★"},
	"settingsModel":       {"⚙"},
	"settingsCredentials": {"✱"},
	"settingsReasoning":   {"≡"},
	"settingsAppearance":  {"◈"},
	"settingsWorkspace":   {"◉"},
	"unsaved":             {"●"},
	"on":                  {"✓"},
	"off":                 {"○"},
	"palette":             {"██"},
	"cursor":              {"┃"},
}

// lookup returns the glyph for the current locale. Panics if name is not in the inventory so a typo is caught immediately during development/testing.
func lookup(name string) string {
	ent, ok := glyphInventory[name]
	if !ok {
		panic("unknown glyph: " + name)
	}
	return ent.utf8
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
