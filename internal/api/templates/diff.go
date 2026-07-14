package templates

import (
	"bufio"
	"strconv"
	"strings"
)

const (
	diffCollapseContext = 2
	diffCollapseMinRun  = diffCollapseContext*2 + 1
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

type renderedDiffRow struct {
	Kind      diffKind
	OldLineNo int
	NewLineNo int
	OldText   string
	NewText   string
}

type diffSection struct {
	Rows        []renderedDiffRow
	Hidden      bool
	HiddenCount int
	GroupID     string
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

	// Reverse stack
	for k := len(stack) - 1; k >= 0; k-- {
		result = append(result, stack[k])
	}

	return result
}

func buildRenderedDiffRows(oldContent, newContent string) []renderedDiffRow {
	diff := computeDiff(splitLines(oldContent), splitLines(newContent))
	rows := make([]renderedDiffRow, 0, len(diff))
	oldLineNo := 1
	newLineNo := 1

	for _, d := range diff {
		row := renderedDiffRow{Kind: d.kind}
		switch d.kind {
		case diffEqual:
			row.OldLineNo = oldLineNo
			row.NewLineNo = newLineNo
			row.OldText = d.line
			row.NewText = d.line
			oldLineNo++
			newLineNo++
		case diffDelete:
			row.OldLineNo = oldLineNo
			row.OldText = d.line
			oldLineNo++
		case diffInsert:
			row.NewLineNo = newLineNo
			row.NewText = d.line
			newLineNo++
		}
		rows = append(rows, row)
	}

	return rows
}

func collapseDiffSections(rows []renderedDiffRow) []diffSection {
	sections := make([]diffSection, 0, len(rows))
	groupIndex := 0

	for i := 0; i < len(rows); {
		if rows[i].Kind != diffEqual {
			j := i + 1
			for j < len(rows) && rows[j].Kind != diffEqual {
				j++
			}
			sections = append(sections, diffSection{Rows: rows[i:j]})
			i = j
			continue
		}

		j := i + 1
		for j < len(rows) && rows[j].Kind == diffEqual {
			j++
		}
		run := rows[i:j]
		if len(run) > diffCollapseMinRun {
			sections = append(sections, diffSection{Rows: run[:diffCollapseContext]})
			groupIndex++
			hiddenRows := run[diffCollapseContext : len(run)-diffCollapseContext]
			sections = append(sections, diffSection{
				Rows:        hiddenRows,
				Hidden:      true,
				HiddenCount: len(hiddenRows),
				GroupID:     "diff-group-" + strconv.Itoa(groupIndex),
			})
			sections = append(sections, diffSection{Rows: run[len(run)-diffCollapseContext:]})
		} else {
			sections = append(sections, diffSection{Rows: run})
		}
		i = j
	}

	return sections
}

func unifiedDiffHTML(oldContent, newContent string) string {
	sections := collapseDiffSections(buildRenderedDiffRows(oldContent, newContent))
	var b strings.Builder
	b.WriteString(`<div class="diff-table diff-table-unified" role="table">`)
	for _, section := range sections {
		if section.Hidden {
			writeCollapseControl(&b, section)
		}
		for _, row := range section.Rows {
			writeUnifiedRow(&b, row, section.Hidden, section.GroupID)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

func sideBySideDiffHTML(oldContent, newContent string) string {
	sections := collapseDiffSections(buildRenderedDiffRows(oldContent, newContent))
	var b strings.Builder
	b.WriteString(`<div class="diff-table diff-table-side-by-side" role="table">`)
	for _, section := range sections {
		if section.Hidden {
			writeCollapseControl(&b, section)
		}
		for _, row := range section.Rows {
			writeSideBySideRow(&b, row, section.Hidden, section.GroupID)
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

func writeCollapseControl(b *strings.Builder, section diffSection) {
	label := "Show " + strconv.Itoa(section.HiddenCount) + " unchanged lines"
	b.WriteString(`<button class="diff-collapse-btn" type="button" data-group="`)
	b.WriteString(section.GroupID)
	b.WriteString(`" data-collapsed-label="`)
	b.WriteString(label)
	b.WriteString(`" data-expanded-label="Hide `)
	b.WriteString(strconv.Itoa(section.HiddenCount))
	b.WriteString(` unchanged lines" aria-expanded="false">`)
	b.WriteString(label)
	b.WriteString(`</button>`)
}

func writeUnifiedRow(b *strings.Builder, row renderedDiffRow, hidden bool, groupID string) {
	b.WriteString(`<div class="diff-row diff-row-`)
	b.WriteString(diffKindName(row.Kind))
	b.WriteString(`"`)
	if hidden {
		b.WriteString(` data-collapse-group="`)
		b.WriteString(groupID)
		b.WriteString(`" hidden`)
	}
	b.WriteString(`>`)
	b.WriteString(`<span class="diff-line-no diff-line-no-old">`)
	b.WriteString(lineNumberText(row.OldLineNo))
	b.WriteString(`</span><span class="diff-line-no diff-line-no-new">`)
	b.WriteString(lineNumberText(row.NewLineNo))
	b.WriteString(`</span><span class="diff-marker">`)
	b.WriteString(diffMarker(row.Kind))
	b.WriteString(`</span><code class="diff-code">`)
	b.WriteString(escapeDiff(unifiedText(row)))
	b.WriteString(`</code></div>`)
}

func writeSideBySideRow(b *strings.Builder, row renderedDiffRow, hidden bool, groupID string) {
	b.WriteString(`<div class="diff-row diff-row-`)
	b.WriteString(diffKindName(row.Kind))
	b.WriteString(`"`)
	if hidden {
		b.WriteString(` data-collapse-group="`)
		b.WriteString(groupID)
		b.WriteString(`" hidden`)
	}
	b.WriteString(`>`)
	writeSideCell(b, "old", row.OldLineNo, sideMarker(row.Kind, true), row.OldText, row.Kind == diffInsert)
	writeSideCell(b, "new", row.NewLineNo, sideMarker(row.Kind, false), row.NewText, row.Kind == diffDelete)
	b.WriteString(`</div>`)
}

func writeSideCell(b *strings.Builder, side string, lineNo int, marker string, text string, empty bool) {
	b.WriteString(`<div class="diff-cell diff-cell-`)
	b.WriteString(side)
	if empty {
		b.WriteString(` diff-cell-empty`)
	}
	b.WriteString(`"><span class="diff-line-no">`)
	b.WriteString(lineNumberText(lineNo))
	b.WriteString(`</span><span class="diff-marker">`)
	b.WriteString(marker)
	b.WriteString(`</span><code class="diff-code">`)
	b.WriteString(escapeDiff(text))
	b.WriteString(`</code></div>`)
}

func unifiedText(row renderedDiffRow) string {
	if row.Kind == diffInsert {
		return row.NewText
	}
	return row.OldText
}

func diffKindName(kind diffKind) string {
	switch kind {
	case diffDelete:
		return "delete"
	case diffInsert:
		return "insert"
	default:
		return "equal"
	}
}

func diffMarker(kind diffKind) string {
	switch kind {
	case diffDelete:
		return "-"
	case diffInsert:
		return "+"
	default:
		return " "
	}
}

func sideMarker(kind diffKind, oldSide bool) string {
	switch kind {
	case diffDelete:
		if oldSide {
			return "-"
		}
		return ""
	case diffInsert:
		if oldSide {
			return ""
		}
		return "+"
	default:
		return " "
	}
}

func lineNumberText(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
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
