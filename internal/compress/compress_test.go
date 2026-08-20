package compress

import (
	"strings"
	"testing"
)

func TestCompressNeverInflates(t *testing.T) {
	t.Parallel()
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

func TestCompressIsDeterministic(t *testing.T) {
	t.Parallel()
	raw := "file1.txt\nfile2.txt\nfile1.txt\nfile3.txt\n"
	a, b := Compress(raw), Compress(raw)
	if a != b {
		t.Fatalf("Compress not deterministic: %q != %q", a, b)
	}
}

func TestCompressIsIdempotent(t *testing.T) {
	t.Parallel()
	raw := buildLongListing(1000)
	first := Compress(raw)
	second := Compress(first)
	if second != first {
		t.Fatalf("Compress not idempotent:\nfirst = %q\nsecond = %q", first, second)
	}
}

func TestCompressStripsANSI(t *testing.T) {
	t.Parallel()
	raw := "\x1b[31mred file\x1b[0m\n\x1b[1mgreen file\x1b[0m\n"
	got := Compress(raw)
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("Compress output still carries ANSI: %q", got)
	}
	if !strings.Contains(got, "red file") || !strings.Contains(got, "green file") {
		t.Fatalf("Compress dropped content: %q", got)
	}
}

func TestCompressDedupesConsecutiveLines(t *testing.T) {
	t.Parallel()
	raw := "a\nb\nb\nb\nc\nc\n"
	got := Compress(raw)
	want := "a\nb\nc\n"
	if got != want {
		t.Fatalf("Compress(%q) = %q, want %q", raw, got, want)
	}
}

func TestCompressTruncatesTailWithExplicitMarker(t *testing.T) {
	t.Parallel()
	n := maxLines + 50
	raw := buildLongListing(n)
	got := Compress(raw)
	if !hasMarker(got) {
		t.Fatalf("Compress output missing explicit tail marker: %q", got)
	}
	if kept := strings.Count(got, "entry."); kept != maxLines {
		t.Fatalf("Compress kept %d entries, want %d", kept, maxLines)
	}
	wantMarker := "+" + itoa(n-maxLines) + " more"
	if !strings.Contains(got, wantMarker) {
		t.Fatalf("Compress output = %q, want it to carry %q", got, wantMarker)
	}
}

func TestCompressHonestEconomics(t *testing.T) {
	t.Parallel()
	raw := buildLongListing(400) // distinct entries, the expensive shape
	compressed := Compress(raw)
	rawTokens := roughTokens(raw)
	compressedTokens := roughTokens(compressed)
	if compressedTokens >= rawTokens {
		t.Fatalf("compression saved nothing: raw=%d tokens, compressed=%d tokens (must be strictly fewer)",
			rawTokens, compressedTokens)
	}
	if len(compressed) >= len(raw) {
		t.Fatalf("expected a real reduction; raw=%d bytes, compressed=%d bytes", len(raw), len(compressed))
	}
}

func TestCompressResultReportsTruth(t *testing.T) {
	t.Parallel()
	raw := buildLongListing(1000)
	if out, compressed := CompressResult(raw); !compressed {
		t.Fatalf("CompressResult(heavy listing) compressed = false, want true")
	} else if out == raw {
		t.Fatalf("CompressResult returned the raw bytes for a compressible input")
	} else if !strings.Contains(out, " more") {
		t.Fatalf("compressed form missing the +N more tail marker: %q", out[len(out)-40:])
	}
	for _, raw := range []string{"", "ok\n", "+300 more\n"} {
		if out, compressed := CompressResult(raw); compressed || out != raw {
			t.Fatalf("CompressResult(%q) = (%q, %v), want raw unchanged and false", raw, out, compressed)
		}
	}
}

func TestCompressScreensProgressFrames(t *testing.T) {
	t.Parallel()
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

func roughTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

func hasMarker(s string) bool {
	trimmed := strings.TrimSuffix(s, "\n")
	if i := strings.LastIndex(trimmed, "\n"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	return markerRE.MatchString(trimmed)
}

func buildLongListing(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("entry.")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	return b.String()
}
