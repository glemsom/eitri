package compress

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// bytesMarkerRe matches the explicit byte-cap tail marker, both the plain form
// (no prior line truncation) and the merged form composed with the
// line-compressor's "+N more" marker.
var bytesMarkerRe = regexp.MustCompile(`(?:\+[0-9]+ more, )?\+[0-9]+ bytes truncated\n$`)

// TestCapBytesUnderBudgetUnchanged verifies a result that fits the byte budget
// passes through byte-identical with zero dropped bytes — and that no marker
// is ever appended (a cap must not inflate a result that already fits).
func TestCapBytesUnderBudgetUnchanged(t *testing.T) {
	cases := []string{
		"",
		"ok\n",
		"README.md\ninternal/pkg_a.go\n+3 more\n",
		strings.Repeat("x", DefaultByteCap),
	}
	for _, draft := range cases {
		got, dropped := CapBytes(draft, DefaultByteCap)
		if got != draft || dropped != 0 {
			t.Errorf("CapBytes(%d bytes, budget=%d) = (%d bytes, dropped=%d), want unchanged (0 dropped)",
				len(draft), DefaultByteCap, len(got), dropped)
		}
	}
}

// TestCapBytesOverBudgetKeepsHeadWithMarker verifies an over-budget result is
// deterministically head-truncated to the budget and carries an explicit
// "+N bytes truncated" marker whose count matches exactly what was dropped.
func TestCapBytesOverBudgetKeepsHeadWithMarker(t *testing.T) {
	draft := strings.Repeat("listing line\n", 30000) // ~390 KiB
	delivered, dropped := CapBytes(draft, DefaultByteCap)

	if len(delivered) <= len(draft) && len(delivered) > DefaultByteCap {
		t.Fatalf("delivered (%d bytes) exceeds the %d-byte budget", len(delivered), DefaultByteCap)
	}
	if !bytesMarkerRe.MatchString(delivered) {
		t.Fatalf("delivered missing explicit byte marker: ...%q", delivered[len(delivered)-40:])
	}
	// The head is a prefix of the draft; the marker line is appended after it.
	body := strings.TrimSuffix(delivered, delivered[len(delivered)-len(bytesMarkerRe.FindString(delivered)):])
	if !strings.HasPrefix(draft, body) {
		t.Fatalf("delivered head %q is not a prefix of the draft", body[:min(len(body), 20)])
	}
	// Dropped bytes: len(draft) - len(kept head); the marker itself is not
	// counted as dropped content.
	if want := len(draft) - len(body); dropped != want {
		t.Fatalf("CapBytes dropped = %d, want %d (raw=%d, kept=%d)", dropped, want, len(draft), len(body))
	}
	if len(delivered) > DefaultByteCap {
		t.Fatalf("delivered = %d bytes, exceeds budget %d", len(delivered), DefaultByteCap)
	}
}

// TestCapBytesComposesWithLineMarker verifies a draft that already ends with
// the line-compressor's "+N more" marker (line-truncated form) byte-truncates
// into a single merged tail line carrying both drop counts, with the marker's
// full length accounted in the budget.
func TestCapBytesComposesWithLineMarker(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("entry.")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	b.WriteString("+300 more\n")
	draft := b.String()

	delivered, dropped := CapBytes(draft, 2000)
	// The merged marker must be the single tail line: both counts visible.
	if !regexp.MustCompile(`\+300 more, \+[0-9]+ bytes truncated\n$`).MatchString(delivered) {
		t.Fatalf("delivered missing merged marker line, tail: %q", delivered[len(delivered)-60:])
	}
	if strings.HasSuffix(delivered, "+300 more\n") {
		t.Fatal("delivered still carries the plain line marker; must be merged with the byte count")
	}
	if len(delivered) > 2000 {
		t.Fatalf("delivered = %d bytes, exceeds 2000-byte budget", len(delivered))
	}
	// Dropped counts bytes of the draft's body only — the line marker's bytes
	// are replaced, not dropped; the "+300 more" line count is preserved.
	body := strings.TrimSuffix(draft, "+300 more\n")
	// delivered = keptHead + merged marker; the kept head is exactly the
	// delivered bytes minus the merged marker line.
	keptHead := len(delivered) - len(bytesMarkerRe.FindString(delivered))
	if want := len(body) - keptHead; dropped != want {
		t.Fatalf("CapBytes dropped = %d, want %d", dropped, want)
	}
}

// TestCapBytesDeterministic verifies the same input and budget always yield
// the same output (protects the byte-stable cache prefix).
func TestCapBytesDeterministic(t *testing.T) {
	draft := strings.Repeat("entry."+itoa(42)+" payload\n", 20000)
	a, da := CapBytes(draft, DefaultByteCap)
	b, db := CapBytes(draft, DefaultByteCap)
	if a != b || da != db {
		t.Fatalf("CapBytes not deterministic: (%d bytes, %d dropped) != (%d bytes, %d dropped)",
			len(a), da, len(b), db)
	}
}

// TestCapBytesIdempotent verifies re-capping a capped result with the same
// budget leaves it alone: the byte marker is never double-marked (the capped
// result is under budget, so the re-cap passes it through byte-identical).
func TestCapBytesIdempotent(t *testing.T) {
	draft := strings.Repeat("entry."+itoa(7)+" payload\n", 20000)
	capped, _ := CapBytes(draft, DefaultByteCap)

	same, redropped := CapBytes(capped, DefaultByteCap)
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

// TestCapBytesUtf8Boundary verifies truncation backs up to a rune boundary:
// the kept head is always valid UTF-8 and no multibyte rune is split by the
// byte cut.
func TestCapBytesUtf8Boundary(t *testing.T) {
	draft := strings.Repeat("héllo wörld\n", 20000) // é/ö are 2-byte runes
	delivered, dropped := CapBytes(draft, DefaultByteCap)

	if !utf8.ValidString(delivered) {
		t.Fatalf("delivered is not valid UTF-8 (cut split a rune)")
	}
	if dropped <= 0 {
		t.Fatalf("expected bytes dropped, got %d", dropped)
	}
	// The body before the marker line must itself be valid UTF-8 and a rune-
	// aligned prefix of the draft (re-encoding never mangles content).
	body := strings.TrimSuffix(delivered, bytesMarkerRe.FindString(delivered))
	if !utf8.ValidString(body) || !strings.HasPrefix(draft, body) {
		t.Fatalf("kept head not a valid-UTF-8 prefix of the draft")
	}
}

// TestCapBytesSlightlyOverBudgetStillTruncates verifies the marker is never
// traded away for size (never-silent): a draft only a few bytes over budget
// still truncates and reports the exact dropped count.
func TestCapBytesSlightlyOverBudgetStillTruncates(t *testing.T) {
	draft := strings.Repeat("x", 1000) // body; no marker to peel
	delivered, dropped := CapBytes(draft, 999)

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
