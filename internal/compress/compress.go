// Package compress implements deterministic, zero-LLM compression of
// high-volume tool output (docs/spec.md §5; ticket T3). It is applied at the
// tool-result boundary so the compressed bytes land in the cache prefix. The
// transform is a pure function: given the same raw string it always yields the
// same output, re-applying never changes it (deterministic + idempotent), and
// it never inflates — if the compressed form is not strictly shorter than the
// raw input, the raw input wins.
package compress

import (
	"regexp"
	"strings"
)

// maxLines caps the number of kept lines before the tail is truncated with an
// explicit "+N more" marker. Kept small enough that long `ls`/`find`/`grep`/
// `rg` reads stay cheap; never silent.
const maxLines = 200

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
		// Explicit "+N more" tail marker — never silent truncation (docs/spec.md §5).
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
