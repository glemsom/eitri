package templates

import (
	"bufio"
	"strings"
)

// diffText produces a simple line-based diff between old and new text.
// Returns HTML-safe diff text with +/- markers.
func diffText(oldContent, newContent string) string {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	// Simple LCS-based diff
	diff := computeDiff(oldLines, newLines)

	var b strings.Builder
	for _, d := range diff {
		switch d.kind {
		case diffEqual:
			b.WriteString(" " + escapeDiff(d.line) + "\n")
		case diffDelete:
			b.WriteString("-" + escapeDiff(d.line) + "\n")
		case diffInsert:
			b.WriteString("+" + escapeDiff(d.line) + "\n")
		}
	}
	return b.String()
}

type diffKind int

const (
	diffEqual diffKind = iota
	diffDelete
	diffInsert
)

type diffLine struct {
	kind diffKind
	line string
}

// computeDiff computes an LCS-based diff between two line slices.
func computeDiff(oldLines, newLines []string) []diffLine {
	m := len(oldLines)
	n := len(newLines)

	// Compute LCS length table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// Backtrack to build diff
	var result []diffLine
	i, j := m, n
	var stack []diffLine

	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			stack = append(stack, diffLine{kind: diffEqual, line: oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			stack = append(stack, diffLine{kind: diffInsert, line: newLines[j-1]})
			j--
		} else if i > 0 {
			stack = append(stack, diffLine{kind: diffDelete, line: oldLines[i-1]})
			i--
		}
	}

	// Reverse the stack
	for k := len(stack) - 1; k >= 0; k-- {
		result = append(result, stack[k])
	}

	return result
}

// splitLines splits text into lines, preserving empty trailing line behavior.
func splitLines(text string) []string {
	if text == "" {
		return []string{""}
	}
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		// Fallback: simple split
		lines = strings.Split(text, "\n")
	}
	// Preserve trailing newline behavior - if text ends with \n, add empty line
	if strings.HasSuffix(text, "\n") {
		lines = append(lines, "")
	}
	return lines
}

// escapeDiff escapes HTML special characters in diff output.
func escapeDiff(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// contextLimit returns a "context window" around changed regions.
// For each changed hunk, includes `context` lines of surrounding context.
// Not yet used - future enhancement for DiffCard.
func contextLimit(lines []diffLine, context int) []diffLine {
	return lines
}
