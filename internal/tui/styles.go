package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// T4 styling identity (issue #122): a restrained dark palette with a single
// agent accent, centralized in one lipgloss style set so the whole surface —
// user chips, agent panes, emoji markers, and the bottom band — draws from
// the same vocabulary. Every color is a hex value: lipgloss adapts hex to the
// terminal's active color profile, so the surface degrades safely to ANSI-256
// (or fewer) colors on a non-truecolor terminal (issue #122 AC5).
var (
	// accentColor is the single agent accent used across the surface.
	accentColor = lipgloss.Color("#7AA2F7")
	// errorColor is the semantic color for failures (⚠ errors, ✗ tool outcomes).
	errorColor = lipgloss.Color("#F7768E")
	// okColor is the semantic color for successful tool outcomes (✓).
	okColor = lipgloss.Color("#9ECE6A")

	// headerStyle is the bold section header (settings title, prompts).
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	// statusStyle is the faint secondary text (strips, hints, tool lines).
	statusStyle = lipgloss.NewStyle().Faint(true)
	// agentPaneStyle frames assistant answers as a left-bordered pane (issue
	// agentPaneStyle frames assistant answers as a left-bordered pane (issue
	// #122 AC1); errorPaneStyle is the same pane with the error-colored border
	// for failing turns so errors read as distinctly as answers.
	agentPaneStyle = borderedPane(accentColor)
	errorPaneStyle = borderedPane(errorColor)
	// thinkingStyle renders the 🤔 collapsed reasoning hint (issue #122 AC2).
	thinkingStyle = lipgloss.NewStyle().Faint(true).Foreground(accentColor)
	// toolStyle renders the ⊕ tool-entry line (issue #122 AC2).
	toolStyle = statusStyle
	// outcomeOKStyle / outcomeErrStyle render the ✓/✗ tool-outcome tags.
	outcomeOKStyle  = lipgloss.NewStyle().Foreground(okColor)
	outcomeErrStyle = lipgloss.NewStyle().Foreground(errorColor)
	// slashSelectStyle highlights the selected slash-completion candidate.
	slashSelectStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	// bandSeparatorStyle draws the separator row that frames the fixed bottom
	// band (status strip + slash completion + composer) as one coherent region
	// (issue #122 AC3). It is a plain separator line, not a lipgloss border:
	// a border pads every band line to the widest row, which would re-pad the
	// composer's own rows and break the band's bottom-pinned layout.
	bandSeparatorStyle = lipgloss.NewStyle().Foreground(accentColor)
)

// borderedPane builds a left-bordered pane with the given border color — the
// shared frame for assistant answers (agent accent) and failing turns (error
// color), keeping the two pane styles from diverging.
func borderedPane(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		PaddingLeft(1).
		BorderForeground(c)
}

// isToolFailure reports whether a delivered tool result is error-shaped: the
// engine surfaces tool failures as plain-text result strings with these
// prefixes (internal/engine/engine.go), so the TUI can tag them ✗ without
// coupling to the engine package's error types.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "error executing tool:") ||
		strings.HasPrefix(result, "invalid tool arguments:")
}
