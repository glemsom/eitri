package tui

import "strings"

// glyph is one production marker in the surface charter.
type glyph struct {
	utf8 string
	tier string // "glyph" | "icon"
}

// glyphInventory is the single source of truth for every production marker.
// Color comes from theme roles, never from the glyph's own presentation.
var glyphInventory = map[string]glyph{
	"toolBash":            {"🐚\ufe0f", "icon"},
	"toolWeb":             {"🌐\ufe0f", "icon"},
	"toolSkill":           {"✨\ufe0f", "icon"},
	"toolGeneric":         {"🧰\ufe0f", "icon"},
	"reasoning":           {"≡", "glyph"},
	"reasoningExpanded":   {"🧠\ufe0f", "icon"},
	"phaseReasoning":      {"🧠\uFE0F", "icon"},
	"phaseWorking":        {"⚒️", "icon"},
	"phaseAnswering":      {"✍️", "icon"},
	"userRole":            {"🧑\ufe0f", "icon"},
	"assistantRole":       {"⚒️", "icon"},
	"brand":               {"⚒️", "icon"},
	"focus":               {"▸", "glyph"},
	"ok":                  {"✓", "glyph"},
	"fail":                {"✗", "glyph"},
	"stopped":             {"⏹", "glyph"},
	"warning":             {"⚠", "glyph"},
	"hr":                  {"─", "glyph"},
	"keyHint":             {"⌨", "glyph"},
	"categoryComposer":    {"✍️", "icon"},
	"categoryNav":         {"🧭️", "icon"},
	"categoryPanes":       {"🪟️", "icon"},
	"categoryActions":     {"⚡️", "icon"},
	"settingsModel":       {"⚙️", "icon"},
	"settingsCredentials": {"🔑️", "icon"},
	"settingsReasoning":   {"🧠️", "icon"},
	"settingsAppearance":  {"🎨️", "icon"},
	"settingsWorkspace":   {"📁️", "icon"},
	"railStats":           {"📊\ufe0f", "icon"},
	"railContext":         {"🧭\ufe0f", "icon"},
	"railModel":           {"⚙️", "icon"},
	"unsaved":             {"●", "glyph"},
	"on":                  {"✓", "glyph"},
	"off":                 {"○", "glyph"},
	"palette":             {"██", "glyph"},
	"cursor":              {"┃", "glyph"},
}

// lookup returns the glyph for the current locale. Panics if name is not in the inventory so a typo is caught immediately during development/testing.
func lookup(name string) string {
	ent, ok := glyphInventory[name]
	if !ok {
		panic("unknown glyph: " + name)
	}
	return ent.utf8
}

// failurePrefix is the error-shaped assistant content prefix ("⚠ ").
func failurePrefix() string { return lookup("warning") + " " }

// stoppedMarker returns the suffix marking a user-stopped turn's partial output ("⏹ stopped"). renderHistory appends it under the stopped message's pane so the aborted turn reads as deliberately stopped, never as an error.
func stoppedMarker() string { return lookup("stopped") + " stopped" }

// toolIcon maps a tool name to its per-tool icon.
func toolIcon(name string) string {
	switch name {
	case "bash":
		return lookup("toolBash")
	case "open_in_browser":
		return lookup("toolWeb")
	case "skill":
		return lookup("toolSkill")
	}
	return lookup("toolGeneric")
}

// userRoleMark returns the 🧑 user role icon.
func userRoleMark() string { return lookup("userRole") }

// assistantRoleMark returns the ⚒️ assistant role icon.
func assistantRoleMark() string { return lookup("assistantRole") }

// brandMark returns the ⚒️ brand icon.
func brandMark() string { return lookup("brand") }

// phaseIcon returns the static phase badge for the forge busy panel.
func phaseIcon(p Phase) string {
	switch p {
	case PhaseReasoning:
		return lookup("phaseReasoning")
	case PhaseAnswering:
		return lookup("phaseAnswering")
	default:
		return lookup("phaseWorking")
	}
}

// focusMarker returns the ▸ cursor glyph prefixing a focused collapsible block's hint/head line.
func focusMarker() string { return lookup("focus") }

// hr returns a horizontal-rule separator (──).
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
