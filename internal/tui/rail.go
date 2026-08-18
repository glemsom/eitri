package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/constants"
)

// Rail enables a fixed-width right pane in the TUI surface: the "true right
// now" state — STATS (cache hit %, cost, turns, token in/out), CONTEXT
// (session id, session temp path), and MODEL (provider/model/effort) —
// rendered alongside, not into, the transcript so the conversation log stays
// clean. It is read-only against the agent loop: the live STATS numbers are
// borrowed from the status-strip Telemetry on the UI goroutine, so the rail
// never pauses or blocks a run.
type Rail struct {
	provider    string
	model       string
	effort      string
	thinking    bool
	sessionID   string
	sessionTemp string
	// branch is the workspace's checked-out git branch (empty when not a
	// worktree), surfaced in the CONTEXT section. Set via SetBranch by the
	// caller after construction.
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

// presizeTerminalWidth is the non-composer fallback terminal width used by the
// Transcript's bandWidth and transcriptWidth before the first window resize
// lands (t.width == 0). TranscriptWidth previously fell back to the composer's
// width here; that coupling was removed so both widths derive solely from the
// terminal width and the rail. Both widths now live on the Transcript.
const presizeTerminalWidth = constants.PresizeTerminalWidth

// line appends one indented rail entry, truncating an over-long row with a
// trailing ellipsis so the rail stays single-line. The usable row width is the
// rail's width minus 2 columns — the left border and the row's leading
// padding — so a row never wraps onto a second line: long session GUIDs / temp
// paths / provider.model names would otherwise fold and break the section
// alignment.
func (r *Rail) line(b *strings.Builder, key, val string, railWidth int) {
	s := "  " + key
	if val != "" {
		s += " " + val
	}
	contentWidth := railWidth - 2
	if lipgloss.Width(s) > contentWidth {
		var sb strings.Builder
		w := 0
		for _, ru := range s {
			if w+1 > contentWidth-1 {
				break
			}
			sb.WriteRune(ru)
			w++
		}
		s = sb.String() + g("…", "...")
	}
	b.WriteString(s + "\n")
}

// railKeyWidth returns the key column width for aligned rail rows at a given
// rail width. Below minWidthRail the default (unpadded) layout is used; above
// it the key column widens in steps so values right-align and the rail reads
// as a stat ledger. The returned width is the total columns occupied by the
// "  " indent plus the key text plus padding, so values start at a consistent
// column when used with lineAligned.
func railKeyWidth(railWidth int) int {
	switch {
	case railWidth >= 55:
		return 16
	case railWidth >= 45:
		return 14
	case railWidth >= minWidthRail:
		return 12
	default:
		return 0 // 0 signals the caller to fall back to the unpadded line()
	}
}

// minWidthRail is the rail width at which aligned key-value rendering kicks in.
// Below this threshold the default unpadded layout is preserved: values are
// space-separated after the key and truncated to the content width.
const minWidthRail = 36

// lineAligned appends one indented rail entry with the key padded to keyWidth
// columns, aligning values at a consistent column for readability at wider
// widths. When keyWidth is 0 it falls back to the unpadded line(). The usable
// row width is railWidth minus 2 (border + indent). Values that would overflow
// are truncated with a trailing ellipsis.
func (r *Rail) lineAligned(b *strings.Builder, key, val string, keyWidth, railWidth int) {
	if keyWidth == 0 {
		r.line(b, key, val, railWidth)
		return
	}
	indent := "  "
	keyCol := indent + key
	// Pad key to keyColWidth columns; if key is already wider, don't pad.
	target := keyColWidth(keyWidth)
	if pw := lipgloss.Width(keyCol); pw < target {
		keyCol += strings.Repeat(" ", target-pw)
	}
	s := keyCol
	if val != "" {
		s += " " + val
	}
	contentWidth := railWidth - 2
	if lipgloss.Width(s) > contentWidth {
		var sb strings.Builder
		w := 0
		for _, ru := range s {
			if w+1 > contentWidth-1 {
				break
			}
			sb.WriteRune(ru)
			w++
		}
		s = sb.String() + g("…", "...")
	}
	b.WriteString(s + "\n")
}

// keyColWidth returns the actual column width for a padded key column. The
// keyWidth parameter is a target; the function ensures a minimum so short keys
// still have breathing room.
func keyColWidth(keyWidth int) int {
	if keyWidth < 8 {
		return 8
	}
	return keyWidth
}

// render returns the rail's rendered STATS/CONTEXT/MODEL block, each
// section tinted with its per-section hue from the theme palette — the header
// bold, the body lines in the same hue — so the sections read apart at a
// glance. It borrows the live status-strip telemetry (te) for the STATS
// numbers, so every value reflects the run's current state; te may be nil
// when no strip is wired, the rail then renders zeroed
// STATS. Rendering stays read-only against the agent loop: it only reads the
// telemetry surface on the UI goroutine.
func (r *Rail) render(te *Telemetry, th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(r.renderStats(te, th, railWidth))
	b.WriteString("\n")
	b.WriteString(r.renderContext(th, railWidth))
	b.WriteString("\n")
	b.WriteString(r.renderModel(th, railWidth))
	return b.String()
}

// renderStats renders the STATS section: the live money/usage picture from the
// telemetry surface as numeric lines only — cache hit %, cost, turns, elapsed
// session time, and token in/out.
func (r *Rail) renderStats(te *Telemetry, th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railStats, "STATS") + "\n")

	hits, misses, out := 0, 0, 0
	turns := 0
	compacted := false
	elapsed := time.Duration(0)
	liveCtx := 0
	if te != nil {
		hits, misses, out = te.cacheHit, te.cacheMiss, te.output
		turns = te.turns
		compacted = te.compacted
		elapsed = time.Since(te.startedAt)
		liveCtx = te.liveContextSize()
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
	kw := railKeyWidth(railWidth)
	r.lineAligned(&body, "cache", fmt.Sprintf("%.0f%%", pct), kw, railWidth)
	r.lineAligned(&body, "cost", formatCost(cost), kw, railWidth)
	r.lineAligned(&body, "turns", fmt.Sprintf("%d/%d", turns, maxTurns), kw, railWidth)
	r.lineAligned(&body, "elapsed", formatElapsed(elapsed), kw, railWidth)
	r.lineAligned(&body, "tokens", fmt.Sprintf("%s in/%s out", formatTokens(totalIn), formatTokens(out)), kw, railWidth)
	// The ctx line reflects the LIVE per-turn context-window size, replaced each
	// usage event and so shrinking after a compaction — unlike the cumulative
	// tokens/cost lines above. It reads via the same formatTokens
	// unit as the tokens line. No live ctx yet (te nil / first turn) renders "0".
	body.WriteString(renderStatsCtxLine(r, th, liveCtx, railWidth) + "\n")
	if compacted {
		r.line(&body, "state", "compacted", railWidth)
	}
	return b.String() + th.railBody(railStats, strings.TrimRight(body.String(), "\n"))
}

// SetBranch records the workspace's checked-out git branch for the CONTEXT
// section. It is a setter (not a NewRail param) so the rail's construction
// signature stays stable across callers without branch context.
func (r *Rail) SetBranch(branch string) { r.branch = branch }

// renderContext renders the CONTEXT section: the active session surface.
func (r *Rail) renderContext(th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railContext, "CONTEXT") + "\n")
	var body strings.Builder
	kw := railKeyWidth(railWidth)
	r.lineAligned(&body, "session", r.sessionID, kw, railWidth)
	r.lineAligned(&body, "temp", r.sessionTemp, kw, railWidth)
	if r.branch != "" {
		r.lineAligned(&body, "branch", r.branch, kw, railWidth)
	}
	return b.String() + th.railBody(railContext, strings.TrimRight(body.String(), "\n"))
}

// renderModel renders the MODEL section: the provider/model/effort surface.
func (r *Rail) renderModel(th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railModel, "MODEL") + "\n")
	var body strings.Builder
	r.line(&body, r.provider+"/"+r.model, "", railWidth)
	effort := r.effort
	if effort == "" {
		effort = "n/a"
	}
	kw := railKeyWidth(railWidth)
	r.lineAligned(&body, "effort", effort, kw, railWidth)
	thinking := "off"
	if r.thinking {
		thinking = "on"
	}
	r.lineAligned(&body, "thinking", thinking, kw, railWidth)
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

// renderStatsCtxLine builds the single STATS ctx line for the live per-turn
// context-window size. It reuses r.line for the same key padding and lets the
// enclosing railBody supply the section's uniform stats hue. When the live size
// reaches the degradation threshold the line flips to warning styling (the
// theme's error hue): a single binary flag, no severity ladder, no latch —
// persistent while live >= threshold.
func renderStatsCtxLine(r *Rail, th Theme, liveCtx, railWidth int) string {
	var b strings.Builder
	kw := railKeyWidth(railWidth)
	r.lineAligned(&b, "ctx", formatTokens(liveCtx), kw, railWidth)
	line := strings.TrimRight(b.String(), "\n")
	if liveCtx >= liveContextWarnThreshold {
		line = lipgloss.NewStyle().Foreground(th.error).Render(line)
	}
	return line
}

// liveContextWarnThreshold is the live context-window size (prompt tokens) at
// which the STATS ctx line flips to warning styling. It is a
// single binary flag once real context reaches ~150k tokens, the point where
// public LLM-context-degradation research (e.g. Anthropic/ZeroWidth/Duper
// long-context studies) reports measurably degraded retrieval-and-reasoning
// quality ~150k+ tokens into a window.
const liveContextWarnThreshold = constants.LiveContextWarnThreshold

// defaultRailWidth is the rail width applied while the Transcript's mutable
// railWidth field is unset (0): consumers read the field through
// railWidthOrDefault, so this constant is only the zero-state default, never a
// width any consumer computes from.
const defaultRailWidth = constants.DefaultRailWidth

// syncWidths re-sizes the composer to the band width so markdown wraps and the
// composer box align with the edge-to-edge bottom band. The composer tracks the
// band (not transcriptWidth) because the band is what frames the composer;
// bandWidth spans the full terminal width under the rail, so the composer input
// line is full-width too. It is called on every window resize and whenever the
// rail toggles visibility. The width source lives on the owned Transcript.
func (m *Model) syncWidths() {
	m.composer.SetWidth(m.tx.bandWidth())
	// A width change re-wraps the draft, so the composer's grown height must
	// track the new soft-wrap layout.
	m.syncComposerHeight()
}

// styledRail frames the rail's rendered sections into a fixed-width right
// column with a left border, so it reads as a distinct state pane alongside the
// transcript. When maxHeight is non-negative it fits the content to exactly that
// many rows so the rail honours the same visible height as the history viewport
// and extends to one row above the band at every terminal height: it TRIMS long
// content (top-aligned, keeping STATS / CONTEXT / start of MODEL and dropping
// the tail) and PADDS short content with blank rows so the rail's left border
// runs down to the band instead of stopping mid-window. A negative maxHeight (no
// resize landed) leaves the rail unclamped and unpadded.
func styledRail(content string, maxHeight, railWidth int) string {
	if maxHeight >= 0 {
		trimmed := strings.TrimRight(content, "\n")
		lines := strings.Split(trimmed, "\n")
		// Target line count so the rail's left border runs down to exactly
		// maxHeight rows (lipgloss adds one borderless blank row above and below
		// the content, so maxHeight-1 content lines put the last bordered row at
		// surface row maxHeight-1 — one row above the band top). Trim long
		// content (top-aligned, dropping the tail) and pad short content with
		// blank rows so the rail fills down to the band at every terminal height,
		// never overlapping it. A negative maxHeight (no resize landed) leaves the
		// rail unclamped and unpadded.
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
