// Package compress implements deterministic, zero-LLM compression of high-volume tool output.
package compress

import (
	"strings"
	"unicode/utf8"

	"github.com/glemsom/eitri/internal/constants"
)

// maxLines caps the number of kept lines before the tail is truncated with an explicit "+N more" marker.
const maxLines = 500

// DefaultByteCap is the shared byte budget every tool result is measured against at the tool-result boundary before it enters message history: the bytes the provider sees and that land in the session-cache head are bounded, so one oversized fetch (via curl) or whole-file read cannot exhaust the context window. 64 KiB fits comfortably inside deepseek's economics — a prompt token is ~3.5 bytes, so a capped result is ~18K tokens, small next to the ~1M-token context — while staying far under the session-cache head that must remain byte-stable.
const DefaultByteCap = constants.DefaultByteCap

// stripTerminalControls removes ECMA-48 terminal controls while preserving ordinary text.
func stripTerminalControls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			if i+size == len(s) {
				i += size
				continue
			}
			switch s[i+size] {
			case '[':
				i = skipCSI(s, i+size+1)
			case ']':
				i = skipStringControl(s, i+size+1, true)
			case 'P', 'X', '^', '_':
				i = skipStringControl(s, i+size+1, false)
			default:
				i = skipEscape(s, i+size)
			}
		case r == 0x9b || s[i] == 0x9b:
			i = skipCSI(s, i+size)
		case r == 0x9d || s[i] == 0x9d:
			i = skipStringControl(s, i+size, true)
		case r == 0x90 || r == 0x98 || r == 0x9e || r == 0x9f || s[i] == 0x90 || s[i] == 0x98 || s[i] == 0x9e || s[i] == 0x9f:
			i = skipStringControl(s, i+size, false)
		case r == 0x9c || s[i] == 0x9c:
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		return i + 1
	}
	return len(s)
}

func skipStringControl(s string, i int, bellTerminates bool) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (bellTerminates && r == 0x07) || r == 0x9c || s[i] == 0x9c {
			return i + size
		}
		if r == 0x1b && i+size < len(s) && s[i+size] == '\\' {
			return i + size + 1
		}
		i += size
	}
	return i
}

func skipEscape(s string, i int) int {
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		return i + 1
	}
	return i
}

func Compress(raw string) string {
	out, _, _ := CompressResult(raw)
	return out
}

func CompressResult(raw string) (text string, compressed bool, dropped int) {
	text = stripTerminalControls(raw)
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

// CapBytes deterministically caps a tool-result draft to a byte budget at the tool-result boundary: over-budget drafts are head-truncated to the budget and an explicit marker line announcing how many bytes were dropped is appended — never silent. upstreamDropped is the count of bytes an earlier memory bound (the sandbox buffer) already rejected; it folds into the one authoritative marker so the final count is never clipped, under-reported, or doubled.
func CapBytes(draft string, budget int, linesDropped int, upstreamDropped int) (delivered string, dropped int) {
	if len(draft) <= budget && upstreamDropped == 0 {
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

	// Reserve marker space for the worst case (every draft byte plus the upstream
	// drop), then keep as much of the head as fits the budget.
	markerReserve := len(merger) + len("+"+itoa(upstreamDropped+len(draft))+" bytes truncated\n")
	keep := budget - markerReserve
	if keep < 0 {
		keep = 0
	}
	if keep > len(draft) {
		keep = len(draft)
	}

	for keep > 0 && keep < len(draft) && !utf8.RuneStart(draft[keep]) {
		keep--
	}

	head := draft[:keep]
	dropped = upstreamDropped + len(draft) - keep

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
