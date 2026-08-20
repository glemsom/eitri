package engine

import "testing"

func TestCapTokensUnderBudgetLeavesTextIntact(t *testing.T) {
	t.Parallel()
	in := "abcdefghijkl"
	got := capTokens(in, 4)
	if got != in {
		t.Fatalf("capTokens(%q, 4) = %q, want the original %q unchanged", in, got, in)
	}
}

func TestCapTokensAtBudgetLeavesTextIntact(t *testing.T) {
	t.Parallel()
	in := "abcdefghijklmnop"
	got := capTokens(in, 4)
	if got != in {
		t.Fatalf("capTokens(%q, 4) = %q, want the boundary-cap text intact (%q)", in, got, in)
	}
	if estimateString(got) != 4 {
		t.Fatalf("estimateString(at-cap result) = %d, want exactly 4 (the budget)", estimateString(got))
	}
}

func TestCapTokensOverBudgetTrimsToBudget(t *testing.T) {
	t.Parallel()
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

func TestCapTokensHandlesNonMultiplesOfFour(t *testing.T) {
	t.Parallel()
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

func TestCapTokensEmptyText(t *testing.T) {
	t.Parallel()
	got := capTokens("", 1)
	if got != "" {
		t.Fatalf("capTokens(\"\", 1) = %q, want empty", got)
	}
}
