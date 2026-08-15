package compress

import (
	"strings"
	"testing"
)

// TestCompressNeverInflates verifies the never-inflate gate:
// when the compressed form is not strictly shorter than the raw input, the raw
// input is returned unchanged. Short, already-terse outputs pass through.
func TestCompressNeverInflates(t *testing.T) {
	cases := []string{
		"",
		"$HOME\n",
		"ok\n",
		"single line\n",
	}
	for _, raw := range cases {
		if got := Compress(raw); got != raw {
			t.Errorf("Compress(%q) = %q, want it unchanged (never-inflate)", raw, got)
		}
	}
}

// TestCompressIsDeterministic verifies the same input always yields the same
// output (protects the byte-stable cache prefix).
func TestCompressIsDeterministic(t *testing.T) {
	raw := "file1.txt\nfile2.txt\nfile1.txt\nfile3.txt\n"
	a, b := Compress(raw), Compress(raw)
	if a != b {
		t.Fatalf("Compress not deterministic: %q != %q", a, b)
	}
}

// TestCompressIsIdempotent verifies re-applying never changes the result: a
// given input collapses on the first pass and stays stable thereafter.
func TestCompressIsIdempotent(t *testing.T) {
	raw := buildLongListing(1000)
	first := Compress(raw)
	second := Compress(first)
	if second != first {
		t.Fatalf("Compress not idempotent:\nfirst = %q\nsecond = %q", first, second)
	}
}

// TestCompressStripsANSI verifies ANSI escape sequences are removed.
func TestCompressStripsANSI(t *testing.T) {
	raw := "\x1b[31mred file\x1b[0m\n\x1b[1mgreen file\x1b[0m\n"
	got := Compress(raw)
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("Compress output still carries ANSI: %q", got)
	}
	if !strings.Contains(got, "red file") || !strings.Contains(got, "green file") {
		t.Fatalf("Compress dropped content: %q", got)
	}
}

// TestCompressDedupesConsecutiveLines verifies repeated consecutive output
// lines collapse to one, keeping order (predictable for an agent).
func TestCompressDedupesConsecutiveLines(t *testing.T) {
	raw := "a\nb\nb\nb\nc\nc\n"
	got := Compress(raw)
	want := "a\nb\nc\n"
	if got != want {
		t.Fatalf("Compress(%q) = %q, want %q", raw, got, want)
	}
}

// TestCompressTruncatesTailWithExplicitMarker verifies heavy listings truncate
// the tail with an explicit "+N more" marker rather than silently dropping it.
func TestCompressTruncatesTailWithExplicitMarker(t *testing.T) {
	n := maxLines + 50
	raw := buildLongListing(n)
	got := Compress(raw)
	if !hasMarker(got) {
		t.Fatalf("Compress output missing explicit tail marker: %q", got)
	}
	// The kept prefix is exactly maxLines distinct entries (dedupe can't collapse
	// them; they differ by index) plus the explicit "+N more" tail marker.
	if kept := strings.Count(got, "entry."); kept != maxLines {
		t.Fatalf("Compress kept %d entries, want %d", kept, maxLines)
	}
	// The marker reports the exact number of dropped lines: never silent, always
	// accurate so the agent knows how much detail the escape must recover.
	wantMarker := "+" + itoa(n-maxLines) + " more"
	if !strings.Contains(got, wantMarker) {
		t.Fatalf("Compress output = %q, want it to carry %q", got, wantMarker)
	}
}

// TestCompressHonestEconomics verifies real, measurable token savings rather
// than a self-reported counter: a noisy listing crosses the compressor in
// fewer billing-shaped tokens than its raw form ("honest
// economics"). Tokens are counted independently, by whitespace/punct splitting
// the way a billing tokenizer would slice a CLI line listing.
func TestCompressHonestEconomics(t *testing.T) {
	raw := buildLongListing(400) // distinct entries, the expensive shape
	compressed := Compress(raw)
	rawTokens := roughTokens(raw)
	compressedTokens := roughTokens(compressed)
	if compressedTokens >= rawTokens {
		t.Fatalf("compression saved nothing: raw=%d tokens, compressed=%d tokens (must be strictly fewer)",
			rawTokens, compressedTokens)
	}
	// Sanity: the compressor must still be the *only* thing between the raw bytes
	// and the model — confirm the raw listing truly was heavy enough to exercise it.
	if len(compressed) >= len(raw) {
		t.Fatalf("expected a real reduction; raw=%d bytes, compressed=%d bytes", len(raw), len(compressed))
	}
}

// TestCompressScreensProgressFrames verifies carriage-return progress redraws
// collapse to their final frame (deterministic, non-inflating).
func TestCompressScreensProgressFrames(t *testing.T) {
	raw := "Downloading 10%...\rDownloading 50%...\rDownloading 100%...\r\nDone\n"
	got := Compress(raw)
	if !strings.Contains(got, "Downloading 100%...") {
		t.Fatalf("Compress should keep the final progress frame, got: %q", got)
	}
	if strings.Contains(got, "Downloading 10%...") || strings.Contains(got, "Downloading 50%...") {
		t.Fatalf("Compress kept stale progress frames: %q", got)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("Compress left raw carriage returns: %q", got)
	}
}

// roughTokens counts billing-shaped tokens as whitespace-delimited words — the
// standard cheap proxy for model tokens on line-oriented CLI output. Independent
// of the compressor's internals.
func roughTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

// hasMarker reports whether s ends with an explicit "+N more" truncation
// marker (the never-silent-truncation signal).
func hasMarker(s string) bool {
	trimmed := strings.TrimSuffix(s, "\n")
	if i := strings.LastIndex(trimmed, "\n"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return markerRE.MatchString(trimmed)
}

// buildLongListing renders a many-line listing of distinct entries, the
// "ls"/"find" output shape that triggers tail truncation.
func buildLongListing(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("entry.")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	return b.String()
}
