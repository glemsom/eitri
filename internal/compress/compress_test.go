package compress

import (
	"strconv"
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

func TestCompressPreservesMarkerLikeInput(t *testing.T) {
	t.Parallel()
	raw := "ordinary output\n+300 more\n"
	got, compressed, dropped := CompressResult(raw)
	if got != raw || compressed || dropped != 0 {
		t.Fatalf("CompressResult(marker-like input) = (%q, %v, %d), want unchanged", got, compressed, dropped)
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

func TestCompressStripsTerminalStringControls(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"osc BEL", "before\x1b]8;;https://example.com\aafter\n", "beforeafter\n"},
		{"osc ST", "before\x1b]0;window title\x1b\\after\n", "beforeafter\n"},
		{"dcs", "before\x1bP$qpayload\x1b\\after\n", "beforeafter\n"},
		{"sos", "before\x1bXpayload\x1b\\after\n", "beforeafter\n"},
		{"pm", "before\x1b^payload\x1b\\after\n", "beforeafter\n"},
		{"apc", "before\x1b_payload\x1b\\after\n", "beforeafter\n"},
		{"unterminated OSC", "before\x1b]0;secret", "before\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compress(tc.raw); got != tc.want {
				t.Fatalf("Compress(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCompressStripsMalformedCSI(t *testing.T) {
	t.Parallel()
	raw := "before\x1b[31\a\rafter"
	if got := Compress(raw); got != "before\n" {
		t.Fatalf("Compress(%q) = %q, want %q", raw, got, "before\n")
	}
}

func TestCompressStripsUTF8C1Controls(t *testing.T) {
	t.Parallel()
	raw := "before\u009b31mred\u009b0mafter\n"
	if got := Compress(raw); got != "beforeredafter\n" {
		t.Fatalf("Compress(%q) = %q, want %q", raw, got, "beforeredafter\n")
	}
}

// A collapse that would cost more to report than it saves is not performed:
// the raw text is returned, so nothing was lost and nothing needs reporting.
func TestCompressSkipsCollapseWhenItWouldInflate(t *testing.T) {
	t.Parallel()
	raw := "a\nb\nb\nb\nc\nc\n"
	if got := Compress(raw); got != raw {
		t.Fatalf("Compress(%q) = %q, want the raw text unchanged", raw, got)
	}
}

// A collapse is a loss, so the Bash seam must report it: the model can only
// recover from a loss it was told about.
func TestCompressReportsCollapsedLines(t *testing.T) {
	t.Parallel()
	// 400 distinct entries, then 600 further copies of the last one.
	raw := buildLongListing(400) + strings.Repeat("entry.399\n", 600)
	got := Compress(raw)

	if want := "+600 repeated lines collapsed"; !strings.Contains(got, want) {
		t.Fatalf("Compress(%d bytes) dropped 600 duplicate lines without reporting it:\n%s", len(raw), got)
	}
	if kept := strings.Count(got, "entry.399"); kept != 1 {
		t.Fatalf("kept %d copies of entry.399, want 1", kept)
	}
}

// CapBytes folds a trailing "+N more" into its byte marker, so the collapse
// report has to land ahead of it or that fold stops matching.
func TestCompressReportsCollapseBeforeTailMarker(t *testing.T) {
	t.Parallel()
	// 550 distinct entries, then 600 more copies of the last one: long enough
	// to overflow maxLines once collapsed, so both markers must appear.
	raw := buildLongListing(maxLines + 50) + strings.Repeat("entry.549\n", 600)
	got := Compress(raw)

	collapseAt := strings.Index(got, "+600 repeated lines collapsed")
	tailAt := strings.Index(got, "+50 more")
	if collapseAt < 0 || tailAt < 0 {
		t.Fatalf("Compress missing a marker (collapse at %d, tail at %d): ...%q",
			collapseAt, tailAt, got[max(0, len(got)-60):])
	}
	if collapseAt > tailAt {
		t.Fatalf("collapse marker must precede the tail marker so CapBytes still folds it")
	}
	if !hasMarker(got) {
		t.Fatalf("last line is no longer the tail marker, CapBytes folding is broken: ...%q", got[len(got)-40:])
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
	wantMarker := "+" + strconv.Itoa(n-maxLines) + " more"
	if !strings.Contains(got, wantMarker) {
		t.Fatalf("Compress output = %q, want it to carry %q", got, wantMarker)
	}
}

func TestCompressHonestEconomics(t *testing.T) {
	t.Parallel()
	raw := buildLongListing(maxLines + 100) // distinct entries, the expensive shape
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
	if out, compressed, _ := CompressResult(raw); !compressed {
		t.Fatalf("CompressResult(heavy listing) compressed = false, want true")
	} else if out == raw {
		t.Fatalf("CompressResult returned the raw bytes for a compressible input")
	} else if !strings.Contains(out, " more") {
		t.Fatalf("compressed form missing the +N more tail marker: %q", out[len(out)-40:])
	}
	for _, raw := range []string{"", "ok\n", "+300 more\n"} {
		if out, compressed, _ := CompressResult(raw); compressed || out != raw {
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
	return strings.HasPrefix(trimmed, "+") && strings.HasSuffix(trimmed, " more")
}

func buildLongListing(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("entry.")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	return b.String()
}
