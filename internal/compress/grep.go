package compress

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// grepMatch holds a single grep match.
type grepMatch struct {
	lineNum int
	content string
}

// compressGrep compresses grep/rg/ripgrep output.
// It parses file:line:content lines, groups by file, and produces a
// summarized output with headers, caps, and truncated lines.
func compressGrep(output string) *string {
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		return nil
	}

	// Parse matches grouped by file.
	type fileMatches struct {
		path    string
		matches []grepMatch
	}
	byFile := make(map[string][]grepMatch)
	fileOrder := make([]string, 0) // preserves insertion order for determinism

	for _, line := range lines {
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}

		// Try to parse as file:line:content.
		m, ok := parseGrepLine(tr)
		if !ok {
			continue
		}

		if _, exists := byFile[m.path]; !exists {
			byFile[m.path] = nil
			fileOrder = append(fileOrder, m.path)
		}
		byFile[m.path] = append(byFile[m.path], grepMatch{lineNum: m.lineNum, content: m.content})
	}

	if len(byFile) == 0 {
		return nil
	}

	// Build list of files with match counts, sorted by count (descending).
	var files []fileMatches
	for _, path := range fileOrder {
		matches := byFile[path]
		// Sort matches by line number.
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].lineNum < matches[j].lineNum
		})
		files = append(files, fileMatches{path: path, matches: matches})
	}

	// Sort files by match count descending; stable by insertion order for ties.
	sort.SliceStable(files, func(i, j int) bool {
		return len(files[i].matches) > len(files[j].matches)
	})

	totalMatches := 0
	for _, f := range files {
		totalMatches += len(f.matches)
	}

	// Cap per file: 10 when total <= 200, else 5.
	capPerFile := 10
	if totalMatches > 200 {
		capPerFile = 5
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d matches in %d files:\n", totalMatches, len(files)))

	for _, f := range files {
		sb.WriteString(fmt.Sprintf("\n%s (%d matches):\n", f.path, len(f.matches)))

		show := f.matches
		if len(show) > capPerFile {
			show = show[:capPerFile]
		}

		for _, m := range show {
			content := truncateLine(m.content, 120)
			sb.WriteString(fmt.Sprintf("  %d: %s\n", m.lineNum, content))
		}

		if len(f.matches) > capPerFile {
			sb.WriteString(fmt.Sprintf("  ... +%d more\n", len(f.matches)-capPerFile))
		}
	}

	result := sb.String()

	// Anti-inflation: if compressed output doesn't reduce estimated tokens,
	// return nil. The caller (Compress) will then use the original output.
	origTokens := len(output) / 4
	compTokens := len(result) / 4
	if compTokens >= origTokens {
		return nil
	}

	return &result
}

// grepLine holds a parsed grep line.
type grepLine struct {
	path    string
	lineNum int
	content string
}

// parseGrepLine attempts to parse a line as file:linenum:content.
// Returns the parsed fields and true on success, or zero value and false.
func parseGrepLine(line string) (grepLine, bool) {
	// A valid grep line must contain at least one '/' or '.' in the path
	// before the first colon. Skip binary file lines.
	if strings.HasPrefix(line, "Binary file ") && strings.HasSuffix(line, " matches") {
		return grepLine{}, false
	}

	// Find the first colon.
	firstColon := strings.IndexByte(line, ':')
	if firstColon < 0 {
		return grepLine{}, false
	}

	path := line[:firstColon]

	// Path must contain at least one '/' or '.' to distinguish from
	// non-grep output.
	hasSep := false
	for _, ch := range path {
		if ch == '/' || ch == '.' {
			hasSep = true
			break
		}
	}
	if !hasSep {
		return grepLine{}, false
	}

	// After the first colon, expect line number then another colon.
	rest := line[firstColon+1:]
	secondColon := strings.IndexByte(rest, ':')
	if secondColon < 0 {
		return grepLine{}, false
	}

	lineNumStr := rest[:secondColon]
	lineNum, err := strconv.Atoi(lineNumStr)
	if err != nil {
		return grepLine{}, false
	}

	content := rest[secondColon+1:]

	return grepLine{
		path:    path,
		lineNum: lineNum,
		content: content,
	}, true
}

// truncateLine truncates a line to maxLen chars, appending "…" if truncated.
func truncateLine(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
