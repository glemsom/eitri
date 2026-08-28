// Package compress implements deterministic, zero-LLM compression of high-volume tool output.
package compress

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/glemsom/eitri/internal/constants"
)

// maxLines caps the number of kept lines before the tail is truncated with an explicit "+N more" marker.
const maxLines = 500

// DefaultByteCap is the shared byte budget every tool result is measured against at the tool-result boundary before it enters message history: the bytes the provider sees and that land in the session-cache head are bounded, so one oversized fetch (via curl) or whole-file read cannot exhaust the context window. 64 KiB fits comfortably inside deepseek's economics — a prompt token is ~3.5 bytes, so a capped result is ~18K tokens, small next to the ~1M-token context — while staying far under the session-cache head that must remain byte-stable.
const DefaultByteCap = constants.DefaultByteCap

// ansiRE matches ANSI/CSI escape sequences that noisy CLI tools emit for color and progress (e.g. `\x1b[31m`, `\x1b[2K`).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// marketRE matches the explicit "+N more" tail marker Eitri itself emits, so a re-apply recognizes an already-truncated listing instead of re-truncating it.
var markerRE = regexp.MustCompile(`^\+[0-9]+ more$`)

// Compress deterministically compresses a tool-result string.
func Compress(raw string) string {
	text := ansiRE.ReplaceAllString(raw, "")
	lines := splitLines(text)
	lines = screenProgressFrames(lines)

	prevMore := 0
	if len(lines) > 0 && markerRE.MatchString(lines[len(lines)-1]) {
		prevMore = atoi(lines[len(lines)-1][1 : len(lines[len(lines)-1])-5])
		lines = lines[:len(lines)-1]
	}

	lines = dedupeConsecutive(lines)

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
		b.WriteByte('+')
		b.WriteString(itoa(more))
		b.WriteString(" more\n")
	}

	if b.Len() >= len(raw) {
		return raw
	}
	return b.String()
}

// CompressResult reports whether Compress actually produced the compressed (truncated) form.
func CompressResult(raw string) (string, bool) {
	out := Compress(raw)
	return out, out != raw
}

// itoa formats a small non-negative integer without pulling fmt into the hot compress path.
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

// atoi parses a small non-negative integer from a decimal string, used to read back an already-emitted "+N more" marker during a re-apply.
func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// lineMarkerRe matches the line-compressor's "+N more" tail marker, anchored at the very end with an optional trailing newline, so the byte-cap can recognize an already-line-truncated draft (markerRE is anchored ^..$ on the marker alone; this one operates on a full draft).
var lineMarkerRe = regexp.MustCompile(`\+([0-9]+) more\n?$`)

// CapBytes deterministically caps a tool-result draft to a byte budget at the tool-result boundary: over-budget drafts are head-truncated to the budget and an explicit marker line announcing how many bytes were dropped is appended — never silent.
func CapBytes(draft string, budget int, lineTruncated bool) (delivered string, dropped int) {
	if len(draft) <= budget {
		return draft, 0
	}

	merger := ""
	if lineTruncated {
		if m := lineMarkerRe.FindString(draft); m != "" {
			draft = draft[:len(draft)-len(m)]
			merger = strings.TrimSuffix(m, "\n") + ", "
		}
	}

	plainMarker := "+" + itoa(len(draft)) + " bytes truncated\n"
	markerReserve := len(merger) + len(plainMarker)
	keep := budget - len(merger) - markerReserve
	if keep < 0 {
		keep = 0
	}
	if keep > len(draft) {
		keep = len(draft)
	}

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

// dedupeConsecutive collapses runs of identical consecutive lines into one, preserving order (so the agent's listing stays readable and predictable).
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

// splitLines splits s on newlines, retaining interior blank lines so blank-separated output survives deterministically.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// screenProgressFrames collapses carriage-return progress output.
func screenProgressFrames(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if i := strings.LastIndex(ln, "\r"); i >= 0 {
			ln = ln[i+1:]
		}
		out = append(out, ln)
	}
	return out
}
