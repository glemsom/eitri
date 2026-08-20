// Package diff is a minimal, dependency-free line diff engine for the TUI's inline card diff (issues #90/#275).
package diff

import "strings"

// Line is one line of a rendered diff hunk: a context, addition, or removal.
type Line struct {
	Type byte
	Text string
}

// Hunk is a contiguous block of changed lines in a file diff, bracketed by unchanged context lines on each side.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []Line
}

// contextRadius is how many unchanged lines frame a hunk on each side.
const contextRadius = 3

// Diff computes a unified-style line diff of old vs. new text.
func Diff(old, new string) []Hunk {
	oldLines := split(old)
	newLines := split(new)
	if equalLines(oldLines, newLines) {
		return nil
	}

	ops := buildOps(oldLines, newLines)

	typed := make([]Line, 0, len(oldLines)+len(newLines))
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			typed = append(typed, Line{' ', op.text})
		case opDelete:
			typed = append(typed, Line{'-', op.text})
		case opInsert:
			typed = append(typed, Line{'+', op.text})
		}
	}

	return groupHunks(typed)
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string // the line text for equal/delete/insert ops
}

// buildOps returns the LCS-derived sequence of equal/delete/insert ops that transforms oldLines into newLines.
func buildOps(oldLines, newLines []string) []op {
	n, m := len(oldLines), len(newLines)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, op{opEqual, oldLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{opDelete, oldLines[i]})
			i++
		default:
			ops = append(ops, op{opInsert, newLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{opDelete, oldLines[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{opInsert, newLines[j]})
	}
	return ops
}

// groupHunks scans the typed line list and assembles hunks: each change or run of nearby changes, bracketed by up to contextRadius unchanged context lines on either side, forms one hunk.
func groupHunks(typed []Line) []Hunk {
	// Annotate every line with its 1-based old/new position.
	type posline struct {
		line Line
		old  int
		new  int
	}
	pl := make([]posline, 0, len(typed))
	oPos, nPos := 1, 1
	for _, l := range typed {
		switch l.Type {
		case ' ':
			pl = append(pl, posline{l, oPos, nPos})
			oPos++
			nPos++
		case '-':
			pl = append(pl, posline{l, oPos, 0})
			oPos++
		case '+':
			pl = append(pl, posline{l, 0, nPos})
			nPos++
		}
	}

	var hunks []Hunk
	i := 0
	prevEnd := 0 // first index not yet consumed; guards against hunk overlap
	for i < len(pl) {
		for i < len(pl) && pl[i].line.Type == ' ' {
			i++
		}
		if i >= len(pl) {
			break
		}
		j := i
		spaceSinceChange := 0
		for j < len(pl) {
			if pl[j].line.Type == ' ' {
				spaceSinceChange++
				if spaceSinceChange > contextRadius {
					break
				}
				j++
				continue
			}
			spaceSinceChange = 0
			j++
		}
		end := j

		start := i
		if start-contextRadius > prevEnd {
			start = i - contextRadius
		} else {
			start = prevEnd
		}
		win := pl[start:end]

		oldStart, newStart := 0, 0
		oldN, newN := 0, 0
		firstOld, firstNew := -1, -1
		for _, p := range win {
			if p.old != 0 {
				oldN++
				if firstOld == -1 {
					firstOld = p.old
				}
			}
			if p.new != 0 {
				newN++
				if firstNew == -1 {
					firstNew = p.new
				}
			}
		}
		if firstOld != -1 {
			oldStart = firstOld
		}
		if firstNew != -1 {
			newStart = firstNew
		}

		lines := make([]Line, 0, len(win))
		for _, p := range win {
			lines = append(lines, p.line)
		}
		hunks = append(hunks, Hunk{
			OldStart: oldStart, OldLines: oldN,
			NewStart: newStart, NewLines: newN,
			Lines: lines,
		})
		prevEnd = end
		i = end
	}
	return hunks
}
