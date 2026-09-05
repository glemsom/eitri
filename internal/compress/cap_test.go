package compress

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

var bytesMarkerRe = regexp.MustCompile(`(?:\+[0-9]+ more, )?\+[0-9]+ bytes truncated\n$`)

func TestCapBytesUnderBudgetUnchanged(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"ok\n",
		"README.md\ninternal/pkg_a.go\n+3 more\n",
		strings.Repeat("x", DefaultByteCap),
	}
	for _, draft := range cases {
		got, dropped := CapBytes(draft, DefaultByteCap, 300)
		if got != draft || dropped != 0 {
			t.Errorf("CapBytes(%d bytes, budget=%d) = (%d bytes, dropped=%d), want unchanged (0 dropped)",
				len(draft), DefaultByteCap, len(got), dropped)
		}
	}
}

func TestCapBytesOverBudgetKeepsHeadWithMarker(t *testing.T) {
	t.Parallel()
	draft := strings.Repeat("listing line\n", 30000) // ~390 KiB
	delivered, dropped := CapBytes(draft, DefaultByteCap, 300)

	if len(delivered) <= len(draft) && len(delivered) > DefaultByteCap {
		t.Fatalf("delivered (%d bytes) exceeds the %d-byte budget", len(delivered), DefaultByteCap)
	}
	if !bytesMarkerRe.MatchString(delivered) {
		t.Fatalf("delivered missing explicit byte marker: ...%q", delivered[len(delivered)-40:])
	}
	body := strings.TrimSuffix(delivered, delivered[len(delivered)-len(bytesMarkerRe.FindString(delivered)):])
	if !strings.HasPrefix(draft, body) {
		t.Fatalf("delivered head %q is not a prefix of the draft", body[:min(len(body), 20)])
	}
	if want := len(draft) - len(body); dropped != want {
		t.Fatalf("CapBytes dropped = %d, want %d (raw=%d, kept=%d)", dropped, want, len(draft), len(body))
	}
	if len(delivered) > DefaultByteCap {
		t.Fatalf("delivered = %d bytes, exceeds budget %d", len(delivered), DefaultByteCap)
	}
}

func TestCapBytesComposesWithLineMarker(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("entry.")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	b.WriteString("+300 more\n")
	draft := b.String()

	delivered, dropped := CapBytes(draft, 2000, 300)
	if !regexp.MustCompile(`\+300 more, \+[0-9]+ bytes truncated\n$`).MatchString(delivered) {
		t.Fatalf("delivered missing merged marker line, tail: %q", delivered[len(delivered)-60:])
	}
	if strings.HasSuffix(delivered, "+300 more\n") {
		t.Fatal("delivered still carries the plain line marker; must be merged with the byte count")
	}
	if len(delivered) > 2000 {
		t.Fatalf("delivered = %d bytes, exceeds 2000-byte budget", len(delivered))
	}
	body := strings.TrimSuffix(draft, "+300 more\n")
	keptHead := len(delivered) - len(bytesMarkerRe.FindString(delivered))
	if want := len(body) - keptHead; dropped != want {
		t.Fatalf("CapBytes dropped = %d, want %d", dropped, want)
	}
}

func TestCapBytesDeterministic(t *testing.T) {
	t.Parallel()
	draft := strings.Repeat("entry."+itoa(42)+" payload\n", 20000)
	a, da := CapBytes(draft, DefaultByteCap, 300)
	b, db := CapBytes(draft, DefaultByteCap, 300)
	if a != b || da != db {
		t.Fatalf("CapBytes not deterministic: (%d bytes, %d dropped) != (%d bytes, %d dropped)",
			len(a), da, len(b), db)
	}
}

func TestCapBytesIdempotent(t *testing.T) {
	t.Parallel()
	draft := strings.Repeat("entry."+itoa(7)+" payload\n", 20000)
	capped, _ := CapBytes(draft, DefaultByteCap, 300)

	same, redropped := CapBytes(capped, DefaultByteCap, 300)
	if same != capped {
		t.Fatalf("re-cap changed the capped result: first=%d bytes, second=%d bytes",
			len(capped), len(same))
	}
	if strings.Count(same, "bytes truncated") > 1 {
		t.Fatalf("re-cap double-marked the result: %q", same[len(same)-80:])
	}
	if redropped != 0 {
		t.Fatalf("re-cap reported %d dropped on an already-capped result, want 0", redropped)
	}
}

func TestCapBytesUtf8Boundary(t *testing.T) {
	t.Parallel()
	draft := strings.Repeat("héllo wörld\n", 20000) // é/ö are 2-byte runes
	delivered, dropped := CapBytes(draft, DefaultByteCap, 300)

	if !utf8.ValidString(delivered) {
		t.Fatalf("delivered is not valid UTF-8 (cut split a rune)")
	}
	if dropped <= 0 {
		t.Fatalf("expected bytes dropped, got %d", dropped)
	}
	body := strings.TrimSuffix(delivered, bytesMarkerRe.FindString(delivered))
	if !utf8.ValidString(body) || !strings.HasPrefix(draft, body) {
		t.Fatalf("kept head not a valid-UTF-8 prefix of the draft")
	}
}

func TestCapBytesSlightlyOverBudgetStillTruncates(t *testing.T) {
	t.Parallel()
	draft := strings.Repeat("x", 1000) // body; no marker to peel
	delivered, dropped := CapBytes(draft, 999, 300)

	if !bytesMarkerRe.MatchString(delivered) {
		t.Fatalf("slightly-over-budget draft must still carry the explicit marker")
	}
	if dropped <= 0 {
		t.Fatalf("dropped = %d, want > 0", dropped)
	}
	body := strings.TrimSuffix(delivered, bytesMarkerRe.FindString(delivered))
	if want := len(draft) - len(body); dropped != want {
		t.Fatalf("dropped = %d, want %d (marker must not inflate the drop count)", dropped, want)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCapBytesMergedMarkerDeliveredNeverExceedsBudget(t *testing.T) {
	t.Parallel()
	budget := 500
	head := strings.Repeat("x", budget+1-len("+29999 more\n"))
	draft := head + "+29999 more\n"
	if len(draft) != budget+1 {
		t.Fatalf("fixture = %d bytes, want budget+1 = %d", len(draft), budget+1)
	}
	delivered, dropped := CapBytes(draft, budget, 29999)
	if len(delivered) > budget {
		t.Fatalf("delivered = %d bytes, exceeds %d-byte budget (merged marker stole from the keep head)",
			len(delivered), budget)
	}
	if !regexp.MustCompile(`\+29999 more, \+[0-9]+ bytes truncated\n$`).MatchString(delivered) {
		t.Fatalf("delivered missing merged marker line, tail: %q", delivered[len(delivered)-50:])
	}
	if dropped <= 0 {
		t.Fatalf("dropped = %d, want > 0", dropped)
	}
}

func TestCapBytesWithoutLineTruncatedNeverMergesLookLikeMarker(t *testing.T) {
	t.Parallel()
	draft := "+300 more\n" + strings.Repeat("content line\n", 30000)
	delivered, _ := CapBytes(draft, DefaultByteCap, 0)
	if strings.Contains(delivered, "+300 more, ") {
		t.Fatalf("raw look-like-marker content merged into a marker: %q", delivered[len(delivered)-60:])
	}
	if !strings.HasSuffix(delivered, " bytes truncated\n") {
		t.Fatalf("delivered missing the plain byte-cap tail: %q", delivered[len(delivered)-60:])
	}
	if !strings.Contains(delivered, "+300 more\n") {
		t.Fatalf("raw look-like-marker content line was silently stripped: %q", delivered[len(delivered)-60:])
	}
}
