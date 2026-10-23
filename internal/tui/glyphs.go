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
	"meterFull":           {"█", "glyph"},
	"meterEmpty":          {"░", "glyph"},
	"cursor":              {"┃", "glyph"},
}

// lookup panics on an unknown name so a mistyped glyph key fails immediately in development.
func lookup(name string) string {
	ent, ok := glyphInventory[name]
	if !ok {
		panic("unknown glyph: " + name)
	}
	return ent.utf8
}

func failurePrefix() string { return lookup("warning") + " " }

// stoppedMarker is appended under a stopped turn's pane so the aborted output reads as deliberately stopped, never as an error.
func stoppedMarker() string { return lookup("stopped") + " stopped" }

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

func userRoleMark() string { return lookup("userRole") }

func assistantRoleMark() string { return lookup("assistantRole") }

func brandMark() string { return lookup("brand") }

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

func hr() string { return lookup("hr") + lookup("hr") }

func hrWidth(w int) string {
	if w < 1 {
		return ""
	}
	return strings.Repeat(lookup("hr"), w)
}

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
