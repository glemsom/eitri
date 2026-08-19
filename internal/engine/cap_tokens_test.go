package engine

import "testing"

// capTokens is the fail-safe that keeps a runaway compaction summary from ever
// growing past the SummaryMaxTokens budget: it truncates text to the first
// n*4 bytes (estimateTokens' char/4 heuristic => exactly n estimated tokens)
// and leaves anything already within budget untouched. These tests pin that
// contract at the pure-function seam with concrete fixtures.

// TestCapTokensUnderBudgetLeavesTextIntact verifies a summary whose estimated
// token count is below the cap is returned byte-for-byte unchanged.
func TestCapTokensUnderBudgetLeavesTextIntact(t *testing.T) {
	t.Parallel()
	// 12 bytes / 4 = 3 estimated tokens, below the n=4 cap (16-byte budget).
	in := "abcdefghijkl"
	got := capTokens(in, 4)
	if got != in {
		t.Fatalf("capTokens(%q, 4) = %q, want the original %q unchanged", in, got, in)
	}
}

// TestCapTokensAtBudgetLeavesTextIntact verifies the boundary: a summary
// exactly at the n-token budget (n*4 bytes) is left intact, not truncated.
func TestCapTokensAtBudgetLeavesTextIntact(t *testing.T) {
	t.Parallel()
	// 16 bytes / 4 = exactly 4 estimated tokens, the boundary for n=4.
	in := "abcdefghijklmnop"
	got := capTokens(in, 4)
	if got != in {
		t.Fatalf("capTokens(%q, 4) = %q, want the boundary-cap text intact (%q)", in, got, in)
	}
	if estimateString(got) != 4 {
		t.Fatalf("estimateString(at-cap result) = %d, want exactly 4 (the budget)", estimateString(got))
	}
}

// TestCapTokensOverBudgetTrimsToBudget verifies a summary that exceeds the cap
// is truncated to the first n*4 bytes (n estimated tokens), never more.
func TestCapTokensOverBudgetTrimsToBudget(t *testing.T) {
	t.Parallel()
	// 20 bytes / 4 = 5 estimated tokens, over the n=4 cap (16-byte budget).
	in := "abcdefghijklmnopqrst"
	got := capTokens(in, 4)
	want := "abcdefghijklmnop" // first 16 bytes = 4 estimated tokens
	if got != want {
		t.Fatalf("capTokens(%q, 4) = %q, want over-budget text trimmed to %q", in, got, want)
	}
	if estimateString(got) != 4 {
		t.Fatalf("estimateString(trimmed result) = %d, want exactly 4 (the budget), never above it", estimateString(got))
	}
}

// TestCapTokensHandlesNonMultiplesOfFour verifies truncation at an arbitrary
// budget that is not a multiple of four bytes: the result is the first n*4
// bytes, whose estimateString still never exceeds n.
func TestCapTokensHandlesNonMultiplesOfFour(t *testing.T) {
	t.Parallel()
	// n=3 => 12-byte budget against a 15-byte input.
	in := "abcdefghijklmno"
	got := capTokens(in, 3)
	want := "abcdefghijkl" // first 12 bytes
	if got != want {
		t.Fatalf("capTokens(%q, 3) = %q, want %q", in, got, want)
	}
	if len(got) > 12 {
		t.Fatalf("trimmed result is %d bytes, exceeds the 12-byte budget", len(got))
	}
	if estimateString(got) > 3 {
		t.Fatalf("estimateString(%q) = %d, exceeds the n=3 budget", got, estimateString(got))
	}
}

// TestCapTokensEmptyText verifies an empty summary is returned unchanged even
// under a tiny budget — the cap never fabricates content.
func TestCapTokensEmptyText(t *testing.T) {
	t.Parallel()
	got := capTokens("", 1)
	if got != "" {
		t.Fatalf("capTokens(\"\", 1) = %q, want empty", got)
	}
}
