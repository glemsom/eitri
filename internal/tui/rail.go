package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Rail enables a fixed-width right pane in the TUI surface (issue #88, Layout A
// "The Ledger"): the "true right now" state — STATS (cache hit %, cost, turns,
// token in/out), CONTEXT (session id, session temp path), SKILLS (detected
// skills with scope + activation state), and MODEL (provider/model/effort) — rendered alongside, not into, the transcript
// so the conversation log stays clean. It is read-only against the agent loop:
// the live STATS numbers are borrowed from the status-strip Telemetry (issue
// #86) on the UI goroutine, so the rail never pauses or blocks a run.
type Rail struct {
	provider    string
	model       string
	effort      string
	thinking    bool
	sessionID   string
	sessionTemp string
}

// NewRail builds the right-context rail seeded with the run's static session
// state (provider, model, effort, thinking, session id, session temp path).
// The caller hands the live Telemetry to render() so STATS stays fresh.
func NewRail(provider, model, effort string, thinking bool, sessionID, sessionTemp string) *Rail {
	return &Rail{
		provider:    provider,
		model:       model,
		effort:      effort,
		thinking:    thinking,
		sessionID:   sessionID,
		sessionTemp: sessionTemp,
	}
}

// line is a helper to append one indented rail entry.
func (r *Rail) line(b *strings.Builder, key, val string) {
	b.WriteString("  " + key + " " + val + "\n")
}

// render returns the rail's rendered STATS/CONTEXT/MODEL block. It borrows the
// live status-strip telemetry (te) for the STATS numbers and the session's
// detected skills (skills) for the SKILLS section, so every value reflects
// the run's current state (issue #88 AC4). te may be nil when no strip is wired;
// the rail then renders zeroed STATS.
func (r *Rail) render(te *Telemetry, skills []SkillItem) string {
	var b strings.Builder

	// STATS — the live money/usage picture from the telemetry surface.
	b.WriteString("STATS\n")
	hits, misses, out := 0, 0, 0
	turns := 0
	compacted := false
	if te != nil {
		hits, misses, out = te.cacheHit, te.cacheMiss, te.output
		turns = te.turns
		compacted = te.compacted
	}
	totalIn := hits + misses
	pct := 0.0
	if totalIn > 0 {
		pct = float64(hits) / float64(totalIn) * 100
	}
	cost := 0.0
	maxTurns := 0
	if te != nil {
		cost = te.cost()
		maxTurns = te.maxTurns
	}
	r.line(&b, "cache", fmt.Sprintf("%.0f%%", pct))
	r.line(&b, "cost", formatCost(cost))
	r.line(&b, "turns", fmt.Sprintf("%d/%d", turns, maxTurns))
	r.line(&b, "tokens", fmt.Sprintf("%s in · %s out", formatTokens(totalIn), formatTokens(out)))
	if compacted {
		r.line(&b, "state", "compacted")
	}
	b.WriteString("\n")

	// CONTEXT — the active session surface.
	b.WriteString("CONTEXT\n")
	r.line(&b, "session", r.sessionID)
	r.line(&b, "temp", r.sessionTemp)
	b.WriteString("\n")

	// SKILLS — detected + per-session active skills (eitri.md §2.3). One line
	// per skill with its install scope and ✓/✕ activation state; the section
	// renders a "none detected" line when the catalog is empty.
	b.WriteString("SKILLS\n")
	if len(skills) == 0 {
		r.line(&b, "none", "detected")
	} else {
		for _, it := range skills {
			state := "✕"
			if it.Active {
				state = "✓"
			}
			r.line(&b, it.Name+" ["+it.Scope+"]", state)
		}
	}
	b.WriteString("\n")

	// MODEL — the provider/model/effort surface.
	b.WriteString("MODEL\n")
	r.line(&b, r.provider+"/"+r.model, "")
	effort := r.effort
	if effort == "" {
		effort = "n/a"
	}
	r.line(&b, "effort", effort)
	thinking := "off"
	if r.thinking {
		thinking = "on"
	}
	r.line(&b, "thinking", thinking)

	return b.String()
}

// formatTokens renders a token count compactly: thousands as X.Xk, millions as
// X.XXM, otherwise the raw integer. It keeps the rail's token readouts short.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// rail constants: railWidth is the fixed right-pane width in columns;
// railShowWidth / railShowHeight are the terminal size below which the rail
// auto-hides, so a pane too narrow or too short to be useful stays off (issue
// #88 AC3).
const (
	railWidth      = 30
	railShowWidth  = 120
	railShowHeight = 24
)

// railVisible reports whether the right context rail should render now. When
// the user has not explicitly toggled it (railAuto), it follows size: shown
// only on windows that are both wide (>= railShowWidth) and tall (>
// railShowHeight) so it never steals vertical room from the fixed bottom band
// on a short terminal (issue T05 AC2). Once the user
// presses ctrl+b the choice becomes explicit (railShown), so the rail toggles
// on any size (issue #88 AC1/AC3).
func (m Model) railVisible() bool {
	if m.rail == nil {
		return false
	}
	if !m.railAuto {
		return m.railShown
	}
	return m.width >= railShowWidth && m.height >= railShowHeight
}

// toggleRail flips the rail's explicit visibility and re-syncs the composer
// width so the transcript takes over the freed space (issue #88).
func (m *Model) toggleRail() {
	// Capture the effective visibility before flipping off auto mode: the
	// toggle always reverses whatever is currently showing, whichever basis
	// (width or explicit) drove it (issue #88 AC1).
	was := m.railVisible()
	m.railAuto = false
	m.railShown = !was
	m.syncWidths()
}

// transcriptWidth returns the column width the left transcript pane should use
// for wrapping: the terminal width (or the composer fallback when no resize has
// landed) minus the 2-col gutter, and minus the rail + separator when the rail
// is visible, so the transcript re-wraps to occupy the freed space (issue #88
// AC3). A floor keeps the pane usable on tiny windows.
func (m Model) transcriptWidth() int {
	base := m.composer.Width() + 2 // back-compat fallback before first resize
	if m.width > 0 {
		base = m.width
	}
	w := base - 2
	if m.railVisible() {
		w -= railWidth + 1
		if w < 20 {
			w = 20
		}
	}
	return w
}

// syncWidths re-sizes the composer to the transcript width so markdown wraps
// and the composer box align with the (possibly rail-shrunk) left pane. It is
// called on every window resize and whenever the rail toggles visibility.
func (m *Model) syncWidths() {
	m.composer.SetWidth(m.transcriptWidth())
	// A width change re-wraps the draft, so the composer's grown height must
	// track the new soft-wrap layout (issue #121 AC5).
	m.syncComposerHeight()
}

// styledRail frames the rail's rendered sections into a fixed-width right
// column with a left border, so it reads as a distinct state pane alongside the
// transcript (Layout A, issue #88). It height-clamps the content to maxHeight
// rows when non-negative so the rail honours the same visible height as the
// history viewport (issue T05): the two panes form one coherent row instead of
// the rail overflowing while the history clips. The clamp keeps the rail's top
// sections (STATS / CONTEXT / start of MODEL) and drops the tail; a negative
// maxHeight (no resize landed) leaves the rail unclamped.
func styledRail(content string, maxHeight int) string {
	if maxHeight >= 0 {
		trimmed := strings.TrimRight(content, "\n")
		lines := strings.Split(trimmed, "\n")
		if maxHeight < len(lines) {
			content = strings.Join(lines[:maxHeight], "\n")
		}
	}
	return lipgloss.NewStyle().
		Width(railWidth).
		PaddingLeft(1).
		Border(lipgloss.Border{Left: "│"}).
		BorderLeft(true).
		Render(strings.TrimRight(content, "\n"))
}
