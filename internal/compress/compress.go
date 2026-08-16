// Package compress implements deterministic, zero-LLM compression of
// high-volume tool output (ticket T3). It is applied at the
// tool-result boundary so the compressed bytes land in the cache prefix. The
// transform is a pure function: given the same raw string it always yields the
// same output, re-applying never changes it (deterministic + idempotent), and
// it never inflates — if the compressed form is not strictly shorter than the
// raw input, the raw input wins.
package compress

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxLines caps the number of kept lines before the tail is truncated with an
// explicit "+N more" marker. Kept small enough that long `ls`/`find`/`grep`/
// `rg` reads stay cheap; never silent.
const maxLines = 200

// DefaultByteCap is the shared byte budget every tool result is measured
// against at the tool-result boundary before it enters message history (issue
// #286): the bytes the provider sees and that land in the session-cache head
// are bounded, so one oversized web_fetch or whole-file read cannot exhaust
// the context window. 64 KiB fits comfortably inside deepseek's economics — a
// prompt token is ~3.5 bytes, so a capped result is ~18K tokens, small next to
// the ~1M-token context — while staying far under the session-cache head that
// must remain byte-stable. It is a single constant, not per-tool limits.
const DefaultByteCap = 64 << 10

// ansiRE matches ANSI/CSI escape sequences that noisy CLI tools emit for
// color and progress (e.g. `\x1b[31m`, `\x1b[2K`). Stripped deterministically
// before line processing.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// marketRE matches the explicit "+N more" tail marker Eitri itself emits, so a
// re-apply recognizes an already-truncated listing instead of re-truncating it.
var markerRE = regexp.MustCompile(`^\+[0-9]+ more$`)

// Compress deterministically compresses a tool-result string. When the
// compressed form is not strictly shorter than the raw input it returns the
// raw input unchanged (the never-inflate gate), so terse outputs pass through
// untouched and no output is silently altered.
func Compress(raw string) string {
	text := ansiRE.ReplaceAllString(raw, "")
	lines := splitLines(text)
	lines = screenProgressFrames(lines)

	// Peel a previously-emitted "+N more" marker so it isn't counted toward the
	// line cap and so its dropped count survives a re-apply (idempotence).
	prevMore := 0
	if len(lines) > 0 && markerRE.MatchString(lines[len(lines)-1]) {
		prevMore = atoi(lines[len(lines)-1][1 : len(lines[len(lines)-1])-5])
		lines = lines[:len(lines)-1]
	}

	lines = dedupeConsecutive(lines)

	// Truncate only a heavy listing; record how many content lines are dropped so
	// the explicit "+N more" marker stays accurate.
	dropped := 0
	if len(lines) > maxLines {
		dropped = len(lines) - maxLines
		lines = lines[:maxLines]
	}

	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	more := prevMore + dropped
	if more > 0 {
		// Explicit "+N more" tail marker — never silent truncation.
		b.WriteByte('+')
		b.WriteString(itoa(more))
		b.WriteString(" more\n")
	}

	// Never-inflate gate: if we gained nothing (or worse), hand back the raw
	// bytes as-is so short/terse output is never bloated or restated.
	if b.Len() >= len(raw) {
		return raw
	}
	return b.String()
}

// CompressResult reports whether Compress actually produced the compressed
// (truncated) form. Callers that need to know whether a string carries the
// line-compressor's "+N more" marker must consult this flag rather than
// pattern-matching the tail: a raw result that merely ends in a line that
// LOOKS like the marker is content, and only the compressor's never-inflate
// gate is the truth about whether the form was compressed (issue #286 review).
func CompressResult(raw string) (string, bool) {
	out := Compress(raw)
	return out, out != raw
}

// itoa formats a small non-negative integer without pulling fmt into the hot
// compress path.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for n := i; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// atoi parses a small non-negative integer from a decimal string, used to read
// back an already-emitted "+N more" marker during a re-apply. The substrings
// it parses are produced only by itoa, so plain ASCII-numeric is guaranteed.
func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// lineMarkerRe matches the line-compressor's "+N more" tail marker, anchored
// at the very end with an optional trailing newline, so the byte-cap can
// recognize an already-line-truncated draft (compress.go's internal markerRE
// is anchored ^..$ on the marker alone; this one operates on a full draft).
var lineMarkerRe = regexp.MustCompile(`\+([0-9]+) more\n?$`)

// CapBytes deterministically caps a tool-result draft to a byte budget at the
// tool-result boundary (issue #286): over-budget drafts are head-truncated to
// the budget and an explicit marker line announcing how many bytes were
// dropped is appended — never silent. It composes with the line-compressor's
// "+N more" tail: when lineTruncated reports the draft really is the
// compressor's output, an already line-truncated draft byte-truncates into a
// single merged tail line carrying both drop counts ("+N more, +B bytes
// truncated"); otherwise a look-alike "+N more" tail is treated as plain
// content and never peeled (issue #286 review).
// Deterministic and idempotent: same input + budget always yields the same
// output, and re-capping a capped result leaves it alone (or re-derives the
// same marker). Truncation backs up to a UTF-8 rune boundary so the kept head
// is always valid UTF-8. The marker's own bytes never count as dropped — B is
// exactly len(draft) - len(kept head), and an under-budget draft passes
// through byte-identical with zero dropped.
//
// budget must be large enough to hold the smallest marker; callers use the
// shared DefaultByteCap.
func CapBytes(draft string, budget int, lineTruncated bool) (delivered string, dropped int) {
	if len(draft) <= budget {
		return draft, 0
	}

	// Detect and strip an existing line-compressor "+N more" tail marker — but
	// ONLY when the caller KNOWS the draft is the line-compressor's output
	// (lineTruncated, set by the engine when bash compressed at the tool
	// boundary). A raw tool result whose last line merely LOOKS like "+N more"
	// (a listing echo, a web page tail) is content, not a marker: peeling and
	// replacing it would silently drop real bytes, so it must never be merged.
	// When present, the merged marker line replaces it (its bytes count against
	// the budget, not as dropped content) so the model sees both drop counts.
	merger := ""
	if lineTruncated {
		if m := lineMarkerRe.FindString(draft); m != "" {
			draft = draft[:len(draft)-len(m)]
			merger = strings.TrimSuffix(m, "\n") + ", "
		}
	}

	// Reserve room for the marker tail inside the budget and keep the
	// head before the cut, so the kept head is always a prefix of the draft. The
	// marker's drop-count digits are unknown until after the cut (dropped =
	// len(stripped) - keep), but dropped <= len(stripped), so reserving for
	// digits(len(stripped)) guarantees the delivered form never exceeds the
	// budget regardless of how many digits the real count needs.
	plainMarker := "+" + itoa(len(draft)) + " bytes truncated\n"
	markerReserve := len(merger) + len(plainMarker)
	keep := budget - len(merger) - markerReserve
	if keep < 0 {
		// Capped to a budget too small to fit even the marker: keep no head, all
		// bytes are dropped (never silent — the marker still reports them).
		keep = 0
	}
	if keep > len(draft) {
		keep = len(draft)
	}

	// Back up to the last UTF-8 rune boundary before the cut so the kept head
	// is always valid UTF-8 (the marker is appended after it, never inside a
	// split rune).
	for keep > 0 && !utf8.RuneStart(draft[keep]) {
		keep--
	}

	head := draft[:keep]
	dropped = len(draft) - keep

	var b strings.Builder
	b.Grow(budget)
	b.WriteString(head)
	b.WriteString(merger)
	b.WriteByte('+')
	b.WriteString(itoa(dropped))
	b.WriteString(" bytes truncated\n")
	return b.String(), dropped
}

// dedupeConsecutive collapses runs of identical consecutive lines into one,
// preserving order (so the agent's listing stays readable and predictable).
func dedupeConsecutive(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[0])
	for i := 1; i < len(lines); i++ {
		if lines[i] != lines[i-1] {
			out = append(out, lines[i])
		}
	}
	return out
}

// splitLines splits s on newlines, retaining interior blank lines so
// blank-separated output survives deterministically. A trailing newline is
// normalized away but re-emitted verbatim by the writer.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// screenProgressFrames collapses carriage-return progress output. Downloaded
// / spinner / build tools redraw a line by returning a carriage return; only
// that *final* frame of a redraw is useful. Any fragment ending in `\r` is a
// redraw frame whose later frames supersede it, so only the final frame of a
// redraw survives. Ordinary lines (no `\r`) pass through intact. Deterministic:
// the last frame of a redraw wins.
func screenProgressFrames(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		// Normalize a trailing `\r` separator (final frame may end in one), then
		// collapse any embedded `\r` redraw frames by keeping the last segment.
		ln = strings.TrimRight(ln, "\r")
		if i := strings.LastIndex(ln, "\r"); i >= 0 {
			ln = ln[i+1:]
		}
		out = append(out, ln)
	}
	return out
}
