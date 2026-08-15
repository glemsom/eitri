package tui

import (
	"fmt"
	"strings"
	"time"

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
	// branch is the workspace's checked-out git branch (empty when not a
	// worktree), surfaced in the CONTEXT section (benchmark §4.1 statusline
	// telemetry). Set via SetBranch by the caller after construction.
	branch string
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

// railContentWidth is the usable column width of a rail row: the fixed rail
// width minus the left border and padding, so a row never wraps onto a second
// line (long session GUIDs / temp paths / provider.model names would
// otherwise fold and break the section alignment).
const railContentWidth = railWidth - 2

// presizeBandWidthDefault is the non-composer fallback terminal width used by
// bandWidth and transcriptWidth before the first window resize lands (m.width ==
// 0). TranscriptWidth previously fell back to the composer's width here; that
// coupling is removed in issue #231 so both widths derive solely from the
// terminal width and the rail.
const presizeBandWidthDefault = 80

// line appends one indented rail entry, truncating an over-long row with a
// trailing ellipsis so the rail stays single-line.
func (r *Rail) line(b *strings.Builder, key, val string) {
	s := "  " + key
	if val != "" {
		s += " " + val
	}
	if lipgloss.Width(s) > railContentWidth {
		var sb strings.Builder
		w := 0
		for _, ru := range s {
			if w+1 > railContentWidth-1 {
				break
			}
			sb.WriteRune(ru)
			w++
		}
		s = sb.String() + g("…", "...")
	}
	b.WriteString(s + "\n")
}

// render returns the rail's rendered STATS/CONTEXT/MODEL block, each
// section tinted with its per-section hue from the theme palette (issue #182
// AC1) — the header bold, the body lines in the same hue — so the sections
// read apart at a glance. It borrows the live status-strip telemetry (te) for
// the STATS numbers, so every value reflects the run's current state (issue
// #88 AC4); te may be nil when no strip is wired, the rail then renders zeroed
// STATS. Rendering stays read-only against the agent loop: it only reads the
// telemetry surface on the UI goroutine.
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
// telemetry surface as numeric lines only — cache hit %, cost, turns, elapsed
// session time, and token in/out (issue #189 removed the usage-history
// sparkline rows; the elapsed readout rounds out the rail as the permanent
// stats surface, issue #227).
func (r *Rail) renderStats(te *Telemetry, th Theme) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railStats, "STATS") + "\n")

	hits, misses, out := 0, 0, 0
	turns := 0
	compacted := false
	elapsed := time.Duration(0)
	if te != nil {
		hits, misses, out = te.cacheHit, te.cacheMiss, te.output
		turns = te.turns
		compacted = te.compacted
		elapsed = time.Since(te.startedAt)
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
	r.line(&body, "elapsed", formatElapsed(elapsed))
	r.line(&body, "tokens", fmt.Sprintf("%s in/%s out", formatTokens(totalIn), formatTokens(out)))
	if compacted {
		r.line(&body, "state", "compacted")
	}
	return b.String() + th.railBody(railStats, strings.TrimRight(body.String(), "\n"))
}

// SetBranch records the workspace's checked-out git branch for the CONTEXT
// section. It is a setter (not a NewRail param) so the rail's construction
// signature stays stable across callers without branch context.
func (r *Rail) SetBranch(branch string) { r.branch = branch }

// renderContext renders the CONTEXT section: the active session surface.
func (r *Rail) renderContext(th Theme) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railContext, "CONTEXT") + "\n")
	var body strings.Builder
	r.line(&body, "session", r.sessionID)
	r.line(&body, "temp", r.sessionTemp)
	if r.branch != "" {
		r.line(&body, "branch", r.branch)
	}
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

// railWidth is the fixed right-pane width in columns.
const railWidth = 30

// railVisible reports whether the right context rail should render now. The
// rail is the sole, permanent stats surface (issue #227): it is always on
// whenever it is wired — no auto-hide on small windows and no ctrl+b toggle.
// The transcript keeps a hard floor via transcriptWidth, so on an
// extreme-minimum terminal the rail yields width so the transcript stays
// readable (issue #227 AC3).
func (m Model) railVisible() bool {
	return m.rail != nil
}

// bandWidth returns the column width the bottom band renders at: the terminal
// width (or a sane non-composer default before the first resize lands) minus the
// 2-col gutter, and minus the rail + separator when the rail is visible, so the
// band sits in the freed rail-shrunk space under the transcript (issue #122 AC3).
// On an extreme-minimum rail-visible terminal it applies the same hard floor as
// transcriptWidth so the separator never collapses below the rail-shrunk read
// width; renderBand clamps a rail-hidden sliver up to 2 separately.
//
// bandWidth is the SEAM for issue #232: it is the single full-width source for
// the edge-to-edge bottom band, deliberately INDEPENDENT of transcriptWidth()
// (it never calls transcriptWidth, and never reads the composer's width). Until
// #232 lands it reproduces the same rail-shrunk number transcriptWidth returns
// so no rendered pixel moves; #232 will drop the rail subtraction below to span
// the full terminal width under the rail.
func (m Model) bandWidth() int {
	base := m.width
	if base == 0 {
		base = presizeBandWidthDefault // no resize yet; use a sane full-width start
	}
	w := base - 2
	if m.railVisible() {
		w -= railWidth + 1
		// Same hard floor transcriptWidth applies (issue #227 AC3): on an
		// extreme-minimum terminal the band's separator must not collapse below
		// the rail-shrunk read width, so bandWidth stays byte-identical to the
		// pre-prefactor value on tiny windows (issue #231 AC3).
		if w < 20 {
			w = 20
		}
	}
	return w
}

// transcriptWidth returns the column width the left transcript pane should use
// for wrapping: the terminal width (or a sane non-composer default when no
// resize has landed) minus the 2-col gutter, and minus the rail + separator when
// the rail is visible, so the transcript re-wraps to occupy the freed space
// (issue #88 AC3). A floor keeps the pane usable on tiny windows. It is the
// dedicated rail-shrunk history/selection width and never reads the composer's
// width (decoupled in issue #231).
func (m Model) transcriptWidth() int {
	base := m.width
	if base == 0 {
		// Back-compat fallback before the first resize: a sane default terminal
		// width, never the composer's width (issue #231 decoupling).
		base = presizeBandWidthDefault
	}
	w := base - 2
	if m.railVisible() {
		w -= railWidth + 1
		// Hard floor (issue #227 AC3): on an extreme-minimum terminal too narrow
		// for the rail to sit beside a full transcript, the rail yields so the
		// transcript pane stays readable rather than being squeezed away.
		if w < 20 {
			w = 20
		}
	}
	return w
}

// syncWidths re-sizes the composer to the band width so markdown wraps and the
// composer box align with the (possibly rail-shrunk) bottom band. The composer
// tracks the band (not transcriptWidth) because the band is what frames the
// composer, and both derive the same rail-shrunk width today (issue #231 seam);
// issue #232 will widen the band across the full terminal width and the
// composer follows. It is called on every window resize and whenever the rail
// toggles visibility.
func (m *Model) syncWidths() {
	m.composer.SetWidth(m.bandWidth())
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
		Border(lipgloss.Border{Left: g("│", "|")}).
		BorderLeft(true).
		Render(strings.TrimRight(content, "\n"))
}
