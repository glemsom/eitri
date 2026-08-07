package templates

import (
	"testing"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/report"
)

// TestSessionIDFromReport verifies the report session-ID extraction helper.
func TestSessionIDFromReport(t *testing.T) {
	if got := sessionIDFromReport(nil); got != "" {
		t.Errorf("sessionIDFromReport(nil) = %q, want empty", got)
	}
	if got := sessionIDFromReport(&report.SessionReport{SessionID: "abc123"}); got != "abc123" {
		t.Errorf("sessionIDFromReport = %q, want abc123", got)
	}
}

// TestFormatNumber verifies the K/M abbreviation helper.
func TestFormatNumber(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1K"},
		{1999, "1K"},
		{1000000, "1M"},
		{2500000, "2M"},
	} {
		if got := formatNumber(tc.in); got != tc.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatDuration verifies the full-run duration formatting helper.
func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "—"},
		{500, "500ms"},
		{1500, "1.5s"},
		{61000, "1m1s"},
		{3600000, "1h0m0s"},
		{3661000, "1h1m0s"},
	} {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatDurationSmall verifies the sub-minute duration formatting helper.
func TestFormatDurationSmall(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "—"},
		{999, "999ms"},
		{1000, "1.0s"},
		{2500, "2.5s"},
	} {
		if got := formatDurationSmall(tc.in); got != tc.want {
			t.Errorf("formatDurationSmall(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatBytes verifies the byte-count formatting helper.
func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0B"},
		{1023, "1023B"},
		{1024, "1KB"},
		{2048, "2KB"},
		{1048576, "1.0MB"},
		{1572864, "1.5MB"},
	} {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRetryLabel verifies the retry badge pluralisation helper.
func TestRetryLabel(t *testing.T) {
	if got := retryLabel(1); got != "1 retry" {
		t.Errorf("retryLabel(1) = %q, want %q", got, "1 retry")
	}
	if got := retryLabel(2); got != "2 retries" {
		t.Errorf("retryLabel(2) = %q, want %q", got, "2 retries")
	}
	if got := retryLabel(0); got != "0 retries" {
		t.Errorf("retryLabel(0) = %q, want %q", got, "0 retries")
	}
}

// TestCacheTokenSuffix verifies the per-call cache token suffix helper.
func TestCacheTokenSuffix(t *testing.T) {
	if got := cacheTokenSuffix(nil); got != "" {
		t.Errorf("cacheTokenSuffix(nil) = %q, want empty", got)
	}
	if got := cacheTokenSuffix(&debug.UsageTotals{}); got != "" {
		t.Errorf("cacheTokenSuffix(empty) = %q, want empty", got)
	}
	if got := cacheTokenSuffix(&debug.UsageTotals{CacheReadTokens: 100}); got != " · 100 cache read" {
		t.Errorf("cacheTokenSuffix(read) = %q, want %q", got, " · 100 cache read")
	}
	if got := cacheTokenSuffix(&debug.UsageTotals{CacheWriteTokens: 42}); got != " · 42 cache created" {
		t.Errorf("cacheTokenSuffix(write) = %q, want %q", got, " · 42 cache created")
	}
	if got := cacheTokenSuffix(&debug.UsageTotals{CacheReadTokens: 100, CacheWriteTokens: 42}); got != " · 100 cache read, 42 cache created" {
		t.Errorf("cacheTokenSuffix(both) = %q", got)
	}
}

// TestCacheSummary verifies the aggregate cache token summary helper.
func TestCacheSummary(t *testing.T) {
	if got := cacheSummary(report.Summary{}); got != "" {
		t.Errorf("cacheSummary(empty) = %q, want empty", got)
	}
	if got := cacheSummary(report.Summary{TotalCacheReadTokens: 1500}); got != "1K read" {
		t.Errorf("cacheSummary(read) = %q, want %q", got, "1K read")
	}
	if got := cacheSummary(report.Summary{TotalCacheReadTokens: 2000, TotalCacheWriteTokens: 300}); got != "2K read / 300 created" {
		t.Errorf("cacheSummary(both) = %q, want %q", got, "2K read / 300 created")
	}
}

// TestRenderJSON verifies the pretty-print JSON helper.
func TestRenderJSON(t *testing.T) {
	if got := renderJSON(nil); got != "null" {
		t.Errorf("renderJSON(nil) = %q, want null", got)
	}
	if got := renderJSON(map[string]any{"a": 1}); got != "{\n  \"a\": 1\n}" {
		t.Errorf("renderJSON(map) = %q", got)
	}
	if got := renderJSON([]any{"x"}); got != "[\n  \"x\"\n]" {
		t.Errorf("renderJSON(slice) = %q", got)
	}
}
