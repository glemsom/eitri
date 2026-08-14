package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Rail enables a fixed-width right pane in the TUI surface (issue #88, Layout A
// "The Ledger"): the "true right now" state — STATS (cache hit %, cost, turns,
// token in/out), CONTEXT (session id, session temp path), and MODEL
// (provider/model/effort) — rendered alongside, not into, the transcript
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

// render returns the rail's rendered STATS/CONTEXT/MODEL block, each
// section tinted with its per-section hue from the theme palette (issue #182
// AC1) — the header bold, the body lines in the same hue — so the sections
// read apart at a glance. It borrows the live status-strip telemetry (te) for
// the STATS numbers, so every value reflects the run's current state (issue
// #88 AC4); te may be nil when no strip is wired, the rail then renders zeroed
// STATS with no sparkline. Rendering stays read-only against the agent loop:
// it only reads the telemetry surface on the UI goroutine.
func (r *Rail) render(te *Telemetry, th Theme) string {
	var b strings.Builder
	b.WriteString(r.renderStats(te, th))
	b.WriteString("\n")
	b.WriteString(r.renderContext(th))
	b.WriteString("\n")
	b.WriteString(r.renderModel(th))
	return b.String()
}

// renderStats renders the STATS section: the live money/usage picture from the
// telemetry surface plus the usage-history sparklines (issue #182 AC2) drawn
// from the same telemetry — per-turn token and cost shapes that update live as
// usage lands.
func (r *Rail) renderStats(te *Telemetry, th Theme) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railStats, "STATS") + "\n")

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
	var body strings.Builder
	r.line(&body, "cache", fmt.Sprintf("%.0f%%", pct))
	r.line(&body, "cost", formatCost(cost))
	r.line(&body, "turns", fmt.Sprintf("%d/%d", turns, maxTurns))
	r.line(&body, "tokens", fmt.Sprintf("%s in · %s out", formatTokens(totalIn), formatTokens(out)))
	if compacted {
		r.line(&body, "state", "compacted")
	}
	if te != nil {
		// The usage-history sparklines: unicode block elements (U+2581..U+2588)
		// so history reads as a shape, not a row of numbers. They are plain
		// glyphs — the shape survives a monochrome terminal, color is only a
		// layer on top (issue #182 AC2/AC5).
		r.line(&body, "usage", te.tokenSparkline(sparkWidth))
		r.line(&body, "cost ", te.costSparkline(sparkWidth))
	}
	return b.String() + th.railBody(railStats, strings.TrimRight(body.String(), "\n"))
}

// renderContext renders the CONTEXT section: the active session surface.
func (r *Rail) renderContext(th Theme) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railContext, "CONTEXT") + "\n")
	var body strings.Builder
	r.line(&body, "session", r.sessionID)
	r.line(&body, "temp", r.sessionTemp)
	return b.String() + th.railBody(railContext, strings.TrimRight(body.String(), "\n"))
}

// renderModel renders the MODEL section: the provider/model/effort surface.
func (r *Rail) renderModel(th Theme) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railModel, "MODEL") + "\n")
	var body strings.Builder
	r.line(&body, r.provider+"/"+r.model, "")
	effort := r.effort
	if effort == "" {
		effort = "n/a"
	}
	r.line(&body, "effort", effort)
	thinking := "off"
	if r.thinking {
		thinking = "on"
	}
	r.line(&body, "thinking", thinking)
	return b.String() + th.railBody(railModel, strings.TrimRight(body.String(), "\n"))
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
	// sparkWidth is the column width of each usage-history sparkline row in
	// STATS (issue #182): the last sparkWidth per-turn samples render as the
	// shape, newest on the right.
	sparkWidth = 12
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
