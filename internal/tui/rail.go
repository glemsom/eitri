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

// presizeTerminalWidth is the non-composer fallback terminal width used by the
// Transcript's bandWidth and transcriptWidth before the first window resize
// lands (t.width == 0). TranscriptWidth previously fell back to the composer's
// width here; that coupling is removed in issue #231 so both widths derive
// solely from the terminal width and the rail. Since issue #247 both widths live
// on the Transcript.
const presizeTerminalWidth = 80

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
	return newTranscript(m).railVisible()
}

// bandWidth returns the column width the bottom band renders at: the terminal
// width (or a sane non-composer default before the first resize lands) minus the
// 2-col gutter. The band is the edge-to-edge bottom region (issue #232): it
// spans the full terminal width all the way under the right rail, so its
// separator row, status strip, and composer run to the width's edge — no
// railWidth x bandHeight dead corner. It is independent of transcriptWidth() in
// the call graph (it never calls transcriptWidth and never reads the composer's
// width).
//
// bandWidth is the SEAM for issue #232: it is the single width source for the
// bottom band, independent of transcriptWidth(). transcriptWidth() stays
// rail-shrunk so the history keeps wrapping to leave the rail room; bandWidth
// does not — it spans full width under the rail.
func (m Model) bandWidth() int {
	return newTranscript(m).bandWidth()
}

// transcriptWidth returns the column width the left transcript pane should use
// for wrapping: the terminal width (or a sane non-composer default when no
// resize has landed) minus the 2-col gutter, and minus the rail + separator when
// the rail is visible, so the transcript re-wraps to occupy the freed space
// (issue #88 AC3). A floor keeps the pane usable on tiny windows. It is the
// dedicated rail-shrunk history/selection width and never reads the composer's
// width (decoupled in issue #231).
func (m Model) transcriptWidth() int {
	return newTranscript(m).transcriptWidth()
}

// syncWidths re-sizes the composer to the band width so markdown wraps and the
// composer box align with the edge-to-edge bottom band (issue #232). The
// composer tracks the band (not transcriptWidth) because the band is what frames
// the composer; bandWidth spans the full terminal width under the rail, so the
// composer input line is full-width too. It is called on every window resize and
// whenever the rail toggles visibility.
func (m *Model) syncWidths() {
	// The composer is a Model-owned concern (issue #248 keeps it there), so
	// syncWidths itself stays on Model; only its width source moved to the
	// Transcript, which now owns bandWidth (issue #247). The composer tracks the
	// band (not transcriptWidth) because the band is what frames the composer;
	// bandWidth spans the full terminal width under the rail.
	m.composer.SetWidth(newTranscript(*m).bandWidth())
	// A width change re-wraps the draft, so the composer's grown height must
	// track the new soft-wrap layout (issue #121 AC5).
	m.syncComposerHeight()
}

// styledRail frames the rail's rendered sections into a fixed-width right
// column with a left border, so it reads as a distinct state pane alongside the
// transcript (Layout A, issue #88). When maxHeight is non-negative it fits the
// content to exactly that many rows so the rail honours the same visible height
// as the history viewport (issue T05) and extends to one row above the band at
// every terminal height (issue #232 AC2): it TRIMS long content (top-aligned,
// keeping STATS / CONTEXT / start of MODEL and dropping the tail) and PADDS short
// content with blank rows so the rail's left border runs down to the band
// instead of stopping mid-window. A negative maxHeight (no resize landed) leaves
// the rail unclamped and unpadded.
func styledRail(content string, maxHeight int) string {
	if maxHeight >= 0 {
		trimmed := strings.TrimRight(content, "\n")
		lines := strings.Split(trimmed, "\n")
		// Target line count so the rail's left border runs down to exactly
		// maxHeight rows (lipgloss adds one borderless blank row above and below
		// the content, so maxHeight-1 content lines put the last bordered row at
		// surface row maxHeight-1 — one row above the band top). Trim long
		// content (top-aligned, dropping the tail) and pad short content with
		// blank rows so the rail fills down to the band at every terminal height,
		// never overlapping it (issue #232 AC2). A negative maxHeight (no resize
		// landed) leaves the rail unclamped and unpadded.
		target := maxHeight - 1
		if target < 1 {
			target = 1
		}
		if len(lines) > target {
			lines = lines[:target]
		} else if len(lines) < target {
			// Pad with single-space blank rows (not empty strings) so they survive
			// the trailing-\n TrimRight before Render and still read as blank lines.
			for len(lines) < target {
				lines = append(lines, " ")
			}
		}
		content = strings.Join(lines, "\n")
	}
	return lipgloss.NewStyle().
		Width(railWidth).
		PaddingLeft(1).
		Border(lipgloss.Border{Left: g("│", "|")}).
		BorderLeft(true).
		Render(strings.TrimRight(content, "\n"))
}
