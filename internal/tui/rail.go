package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/glemsom/eitri/internal/constants"
)

type Rail struct {
	provider    string
	model       string
	effort      string
	thinking    bool
	sessionID   string
	sessionTemp string
	branch      string
	sessionKey  *LiveSessionKey
}

// NewRail builds the right-context rail seeded with the run's static session state (provider, model, effort, thinking, session id, session temp path).
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

// presizeTerminalWidth is the non-composer fallback terminal width used by the Transcript's bandWidth and transcriptWidth before the first window resize lands (t.width == 0).
const presizeTerminalWidth = constants.PresizeTerminalWidth

// line appends one indented rail entry, truncating an over-long row with a trailing ellipsis so the rail stays single-line.
func (r *Rail) line(b *strings.Builder, key, val string, railWidth int) {
	s := "  " + key
	if val != "" {
		s += " " + val
	}
	contentWidth := railWidth - 2
	if lipgloss.Width(s) > contentWidth {
		s = r.truncateCellWidth(s, contentWidth-1) + g("…", "...")
	}
	b.WriteString(s + "\n")
}

// truncateCellWidth keeps the longest rune prefix of s whose display-cell width
// fits the budget; the caller appends the trailing ellipsis.
func (r *Rail) truncateCellWidth(s string, budget int) string {
	var sb strings.Builder
	w := 0
	for _, ru := range s {
		w += ansi.StringWidth(string(ru))
		if w > budget {
			break
		}
		sb.WriteRune(ru)
	}
	return sb.String()
}

// railKeyWidth returns the key column width for aligned rail rows at a given rail width.
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
const minWidthRail = 36

// lineAligned appends one indented rail entry with the key padded to keyWidth columns, aligning values at a consistent column for readability at wider widths.
func (r *Rail) lineAligned(b *strings.Builder, key, val string, keyWidth, railWidth int) {
	if keyWidth == 0 {
		r.line(b, key, val, railWidth)
		return
	}
	indent := "  "
	keyCol := indent + key
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
		s = r.truncateCellWidth(s, contentWidth-1) + g("…", "...")
	}
	b.WriteString(s + "\n")
}

// keyColWidth returns the actual column width for a padded key column.
func keyColWidth(keyWidth int) int {
	if keyWidth < 8 {
		return 8
	}
	return keyWidth
}

// render returns the rail's rendered sections for tests and non-live callers.
func (r *Rail) render(te *Telemetry, th Theme, railWidth int) string {
	return r.renderLive(te, th, railWidth, PhaseIdle, 0)
}

// renderLive returns the rail's rendered STATS/CONTEXT/MODEL block, each section tinted with its per-section hue from the theme palette — the header bold, the body lines in the same hue — so the sections read apart at a glance.
func (r *Rail) renderLive(te *Telemetry, th Theme, railWidth int, phase Phase, spinner int) string {
	return r.renderLiveWithTools(te, th, railWidth, phase, spinner, nil)
}

func (r *Rail) renderLiveWithTools(te *Telemetry, th Theme, railWidth int, phase Phase, spinner int, log *toolLog) string {
	var b strings.Builder
	b.WriteString(r.renderStats(te, th, railWidth))
	b.WriteString("\n")
	b.WriteString(r.renderContext(th, railWidth))
	b.WriteString("\n")
	b.WriteString(r.renderModel(th, railWidth))
	return b.String()
}

// renderStats renders the STATS section: the live usage picture from the telemetry surface as numeric lines only — cache hit %, turns, elapsed session time, and token in/out.
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
	maxTurns := 0
	if te != nil {
		maxTurns = te.maxTurns
	}
	var body strings.Builder
	kw := railKeyWidth(railWidth)
	r.lineAligned(&body, "cache", fmt.Sprintf("%.0f%% %s", pct, railMeter(pct/100, railWidth)), kw, railWidth)
	r.lineAligned(&body, "turns", fmt.Sprintf("%d/%d", turns, maxTurns), kw, railWidth)
	r.lineAligned(&body, "elapsed", formatElapsed(elapsed), kw, railWidth)
	r.lineAligned(&body, "tokens", fmt.Sprintf("%s in/%s out", formatTokens(totalIn), formatTokens(out)), kw, railWidth)
	body.WriteString(renderStatsCtxLine(r, th, liveCtx, railWidth) + "\n")
	if compacted {
		r.line(&body, "recovery", "context overflow", railWidth)
	}
	return b.String() + th.railBody(railStats, strings.TrimRight(body.String(), "\n"))
}

// SetBranch records the workspace's checked-out git branch for the CONTEXT section.
func (r *Rail) SetBranch(branch string) { r.branch = branch }

// SetLiveKey wires the shared mutable session key into the rail, so the
// CONTEXT session id stays live across a `/new` re-mint. Nil keeps the static
// sessionID seeded at construction.
func (r *Rail) SetLiveKey(l *LiveSessionKey) { r.sessionKey = l }

func (r *Rail) currentSessionTemp(sessionID string) string {
	if r.sessionKey == nil || sessionID == r.sessionID || r.sessionTemp == "" {
		return r.sessionTemp
	}
	clean := filepath.Clean(r.sessionTemp)
	if filepath.Base(clean) == r.sessionID {
		return filepath.Join(filepath.Dir(clean), sessionID)
	}
	parent := filepath.Dir(clean)
	if filepath.Base(parent) == r.sessionID {
		return filepath.Join(filepath.Dir(parent), sessionID, filepath.Base(clean))
	}
	return r.sessionTemp
}

// renderContext renders the CONTEXT section: the active session surface.
func (r *Rail) renderContext(th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railContext, "CONTEXT") + "\n")
	var body strings.Builder
	kw := railKeyWidth(railWidth)
	ts := r.sessionID
	if r.sessionKey != nil {
		ts = r.sessionKey.Get()
	}
	r.lineAligned(&body, "session", ts, kw, railWidth)
	r.lineAligned(&body, "temp", r.currentSessionTemp(ts), kw, railWidth)
	if r.branch != "" {
		r.lineAligned(&body, "branch", r.branch, kw, railWidth)
	}
	return b.String() + th.railBody(railContext, strings.TrimRight(body.String(), "\n"))
}

// renderModel renders the MODEL section with provider/model hierarchy and compact mode badges.
func (r *Rail) renderModel(th Theme, railWidth int) string {
	var b strings.Builder
	b.WriteString(th.railHeader(railModel, "MODEL") + "\n")
	var body strings.Builder
	kw := railKeyWidth(railWidth)
	r.lineAligned(&body, "provider", r.provider, kw, railWidth)
	r.lineAligned(&body, "model", r.model, kw, railWidth)
	effort := r.effort
	if effort == "" {
		effort = "n/a"
	}
	thinking := "think:off"
	if r.thinking {
		thinking = "think:on"
	}
	r.lineAligned(&body, "mode", fmt.Sprintf("effort:%s %s", effort, thinking), kw, railWidth)
	return b.String() + th.railBody(railModel, strings.TrimRight(body.String(), "\n"))
}

// formatTokens renders a token count compactly: thousands as X.Xk, millions as X.XXM, otherwise the raw integer.
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

// renderStatsCtxLine builds the single STATS ctx line for the live per-turn context-window size.
func renderStatsCtxLine(r *Rail, th Theme, liveCtx, railWidth int) string {
	var b strings.Builder
	kw := railKeyWidth(railWidth)
	val := formatTokens(liveCtx)
	if liveCtx > 0 {
		val += " " + railMeter(float64(liveCtx)/float64(liveContextWarnThreshold), railWidth)
	}
	r.lineAligned(&b, "ctx", val, kw, railWidth)
	line := strings.TrimRight(b.String(), "\n")
	if liveCtx >= liveContextWarnThreshold {
		line = lipgloss.NewStyle().Foreground(th.error).Render(line)
	}
	return line
}

func railMeter(fraction float64, railWidth int) string {
	width := 6
	if railWidth >= 45 {
		width = 10
	}
	if railWidth >= 60 {
		width = 14
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	return "[" + strings.Repeat("=", filled) + strings.Repeat("-", width-filled) + "]"
}

// liveContextWarnThreshold is the live context-window size (prompt tokens) at which the STATS ctx line flips to warning styling.
const liveContextWarnThreshold = constants.LiveContextWarnThreshold

// defaultRailWidth is the rail width applied while the Transcript's mutable railWidth field is unset (0): consumers read the field through railWidthOrDefault, so this constant is only the zero-state default, never a width any consumer computes from.
const defaultRailWidth = constants.DefaultRailWidth

// syncWidths re-sizes the composer to the panel body width so markdown wraps inside the edge-to-edge bottom band.
func (m *Model) syncWidths() {
	m.composer.SetWidth(composerPanelBodyWidth(m.tx.bandWidth()))
	m.syncComposerHeight()
}

// adjustRailWidth nudges the rail width by delta (clamped to minWidthRail) and persists it, so ctrl+x/z resize the right pane across sessions.
func (m *Model) adjustRailWidth(delta int) {
	w := m.tx.railWidthOrDefault() + delta
	if w < minWidthRail {
		w = minWidthRail
	}
	m.tx.setRailWidth(w)
	m.persistRailWidth()
	m.syncWidths()
}

// persistRailWidth writes the current rail width into deps.Config and persists it via the Save seam so the width round-trips across sessions.
func (m *Model) persistRailWidth() {
	m.deps.Config.RailWidth = m.tx.railWidthOrDefault()
	if m.deps.Save != nil {
		_ = m.deps.Save(m.deps.Config)
	}
}

// styledRail frames the rail's rendered sections into a fixed-width right column with a left border, so it reads as a distinct state pane alongside the transcript.
func styledRail(content string, maxHeight, railWidth int) string {
	if maxHeight >= 0 {
		trimmed := strings.TrimRight(content, "\n")
		lines := strings.Split(trimmed, "\n")
		target := maxHeight - 1
		if target < 1 {
			target = 1
		}
		if len(lines) > target {
			lines = lines[:target]
		} else if len(lines) < target {
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

const railFaceTopGap = 1

func styledRailWithFace(content string, maxHeight, railWidth int) string {
	_, faceRows := railFaceRows(railWidth)
	textRows := railContentRows(content)
	reservedRows := textRows + railFaceTopGap + faceRows
	if maxHeight < reservedRows || !kittyGraphicsLikelySupported() {
		return styledRail(content, maxHeight, railWidth)
	}
	return styledRail(content, textRows+railFaceTopGap+1, railWidth) + "\n" + styledFaceRail(faceRows, railWidth) +
		"\n" + styledFaceRail(maxHeight-reservedRows, railWidth)
}

func railContentRows(content string) int {
	return len(strings.Split(strings.TrimRight(content, "\n"), "\n"))
}

func styledFaceRail(faceRows, railWidth int) string {
	var b strings.Builder
	row := g("│", "|") + strings.Repeat(" ", max(0, railWidth-1))
	for i := 0; i < faceRows; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(row)
	}
	return b.String()
}
