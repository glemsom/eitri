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

func Compress(raw string) string {
	out, _, _ := CompressResult(raw)
	return out
}

func CompressResult(raw string) (text string, compressed bool, dropped int) {
	text = ansiRE.ReplaceAllString(raw, "")
	lines := splitLines(text)
	lines = screenProgressFrames(lines)

	lines = dedupeConsecutive(lines)

	dropped = 0
	if len(lines) > maxLines {
		dropped = len(lines) - maxLines
		lines = lines[:maxLines]
	}

	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	more := dropped
	if more > 0 {
		b.WriteByte('+')
		b.WriteString(itoa(more))
		b.WriteString(" more\n")
	}

	if b.Len() >= len(raw) {
		return raw, false, 0
	}
	return b.String(), true, more
}

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

// CapBytes deterministically caps a tool-result draft to a byte budget at the tool-result boundary: over-budget drafts are head-truncated to the budget and an explicit marker line announcing how many bytes were dropped is appended — never silent.
func CapBytes(draft string, budget int, linesDropped int) (delivered string, dropped int) {
	if len(draft) <= budget {
		return draft, 0
	}

	merger := ""
	if linesDropped > 0 {
		lineMarker := "+" + itoa(linesDropped) + " more\n"
		if strings.HasSuffix(draft, lineMarker) {
			draft = strings.TrimSuffix(draft, lineMarker)
			merger = strings.TrimSuffix(lineMarker, "\n") + ", "
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

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

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
