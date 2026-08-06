package templates

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/report"
)

// sessionIDFromReport returns the session ID from the report.
func sessionIDFromReport(rep *report.SessionReport) string {
	if rep == nil {
		return ""
	}
	return rep.SessionID
}

// formatNumber abbreviates a large integer with an M/K suffix.
func formatNumber(n int) string {
	if n >= 1000000 {
		return strconv.Itoa(n/1000000) + "M"
	}
	if n >= 1000 {
		return strconv.Itoa(n/1000) + "K"
	}
	return strconv.Itoa(n)
}

// formatDuration renders a millisecond duration as a compact human string.
func formatDuration(ms int64) string {
	if ms == 0 {
		return "—"
	}
	d := time.Duration(ms) * time.Millisecond
	if d >= time.Hour {
		return d.Round(time.Minute).String()
	}
	if d >= time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Millisecond).String()
}

// formatDurationSmall renders a sub-minute millisecond duration compactly.
func formatDurationSmall(ms int64) string {
	if ms == 0 {
		return "—"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}

// formatBytes renders a byte count with a compact unit suffix.
func formatBytes(b int) string {
	if b < 1024 {
		return strconv.Itoa(b) + "B"
	}
	if b < 1024*1024 {
		return strconv.Itoa(b/1024) + "KB"
	}
	return fmt.Sprintf("%.1fMB", float64(b)/(1024.0*1024.0))
}

// retryLabel renders the retry badge text for a turn, e.g. "1 retry" or "2 retries".
func retryLabel(failed int) string {
	if failed == 1 {
		return "1 retry"
	}
	return strconv.Itoa(failed) + " retries"
}

// cacheTokenSuffix returns a human-readable suffix describing the provider's
// cache-read/cache-creation token counts, or "" when neither is reported.
func cacheTokenSuffix(u *debug.UsageTotals) string {
	if u == nil {
		return ""
	}
	var parts []string
	if u.CacheReadTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d cache read", u.CacheReadTokens))
	}
	if u.CacheWriteTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d cache created", u.CacheWriteTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, ", ")
}

// cacheSummary renders the aggregate cache token counts for a run summary.
func cacheSummary(s report.Summary) string {
	var parts []string
	if s.TotalCacheReadTokens > 0 {
		parts = append(parts, formatNumber(s.TotalCacheReadTokens)+" read")
	}
	if s.TotalCacheWriteTokens > 0 {
		parts = append(parts, formatNumber(s.TotalCacheWriteTokens)+" created")
	}
	return strings.Join(parts, " / ")
}

// contextPercent returns the rounded percentage of the context window in use,
// clamped to 100.
func contextPercent(ci *report.ContextInfo) int {
	if ci == nil || ci.ContextWindow == 0 {
		return 0
	}
	pct := ci.TotalTokens * 100 / ci.ContextWindow
	if pct > 100 {
		pct = 100
	}
	return pct
}

// renderJSON pretty-prints an arbitrary value as indented JSON for display.
func renderJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}
