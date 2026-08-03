package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

const maxGrepOutputBytes = 2 * 1024

type grepArgs struct {
	Pattern     string `json:"pattern" jsonschema:"Go regex (RE2 syntax) pattern to search for in file contents."`
	FilePattern string `json:"file_pattern,omitempty" jsonschema:"Optional glob pattern to filter files by path relative to workspace root (e.g. '*.go' to search only Go files)."`

	Context int `json:"context,omitempty" jsonschema:"Number of surrounding context lines to include before and after each match (default 0)." jsonschema_minimum:"0"`
}

// GrepTool implements ToolHandler for searching file contents with regex.
type GrepTool struct {
	workspace string
	schema    litellm.Schema
}

// NewGrepTool creates a new GrepTool.
func NewGrepTool(workspace string) *GrepTool {
	return &GrepTool{
		workspace: workspace,
		schema:    SchemaOf[grepArgs](),
	}
}

func (t *GrepTool) Name() string {
	return "grep"
}

func (t *GrepTool) Description() string {
	return "Search file contents by regex (RE2). Filter files with file_pattern glob. context=N shows N surrounding lines; match lines prefixed >. Output: file:line:content. Capped at 2 KiB."
}

func (t *GrepTool) JSONSchema() litellm.Schema {
	return t.schema
}

// grepLine is one rendered output line: "path:line:content" (with a ">" prefix
// on match lines when context is enabled).
type grepLine struct {
	path    string
	lineNum int
	content string
	isMatch bool
}

// lineRange is an inclusive, 0-indexed line range within a file.
type lineRange struct{ start, end int }

// contextWindows returns the merged, non-overlapping line ranges within
// contextN lines of any match. matchNums are 1-indexed match line numbers in
// ascending order; numLines is the total number of lines in the file.
// Overlapping or adjacent windows are merged into a single contiguous range so
// every line within reach of a match is rendered exactly once.
func contextWindows(matchNums []int, numLines, contextN int) []lineRange {
	if len(matchNums) == 0 {
		return nil
	}
	lastIdx := numLines - 1
	var windows []lineRange
	for _, matchNum := range matchNums {
		start := matchNum - 1 - contextN
		if start < 0 {
			start = 0
		}
		end := matchNum - 1 + contextN
		if end > lastIdx {
			end = lastIdx
		}
		if n := len(windows); n > 0 && start <= windows[n-1].end {
			if end > windows[n-1].end {
				windows[n-1].end = end
			}
		} else {
			windows = append(windows, lineRange{start: start, end: end})
		}
	}
	return windows
}

// collectGrepLines scans fileLines (the result of strings.Split on '\n') for
// regex matches and returns the output lines to render, or nil when the file
// has no matches. With contextN > 0 only lines within contextN of a match are
// retained (merged across matches); otherwise only the matching lines are
// returned. Files with no matches yield nil so callers retain nothing for them.
func collectGrepLines(fileLines []string, re *regexp.Regexp, contextN int) []grepLine {
	var matchNums []int
	for i, line := range fileLines {
		if re.MatchString(line) {
			matchNums = append(matchNums, i+1)
		}
	}
	if len(matchNums) == 0 {
		return nil
	}

	if contextN == 0 {
		lines := make([]grepLine, 0, len(matchNums))
		for _, matchNum := range matchNums {
			// No ">" prefix in context=0 mode, so isMatch stays false.
			lines = append(lines, grepLine{lineNum: matchNum, content: fileLines[matchNum-1]})
		}
		return lines
	}

	var lines []grepLine
	matchIdx := 0
	for _, w := range contextWindows(matchNums, len(fileLines), contextN) {
		for i := w.start; i <= w.end; i++ {
			lineNum := i + 1
			isMatch := matchIdx < len(matchNums) && matchNums[matchIdx] == lineNum
			if isMatch {
				matchIdx++
			}
			lines = append(lines, grepLine{lineNum: lineNum, content: fileLines[i], isMatch: isMatch})
		}
	}
	return lines
}

// grepLineSize returns the number of bytes a rendered line occupies in the
// "path:line:content\n" format (plus the ">" match prefix in context mode).
func grepLineSize(l grepLine) int {
	size := len(l.path) + 1 + len(strconv.Itoa(l.lineNum)) + 1 + len(l.content) + 1
	if l.isMatch {
		size++
	}
	return size
}

func (t *GrepTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("grep: invalid args: %w", err)
	}

	if parsed.Pattern == "" {
		return ToolError(TextBlocks("Error: pattern is required")), nil
	}

	re, err := regexp.Compile(parsed.Pattern)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: invalid regex %q: %v", parsed.Pattern, err))), nil
	}

	contextN := parsed.Context
	if contextN < 0 {
		contextN = 0
	}

	var (
		out        []grepLine
		outputSize int
		truncated  bool
	)

	err = fileutil.WalkWorkspace(t.workspace, func(path, relPath string, d os.DirEntry) error {
		var data []byte
		data, err = os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.ContainsRune(string(data), '\x00') {
			return nil
		}

		fileLines := collectGrepLines(strings.Split(string(data), "\n"), re, contextN)
		if fileLines == nil {
			// No matches: nothing about this file is retained.
			return nil
		}

		for _, l := range fileLines {
			l.path = relPath
			lineSize := grepLineSize(l)
			// Byte accounting happens here, in the walk, for both context=0
			// and context>0 modes so the walk stops as soon as the cap is hit.
			if outputSize+lineSize > maxGrepOutputBytes && len(out) > 0 {
				truncated = true
				return &fileutil.WalkStop{}
			}
			outputSize += lineSize
			out = append(out, l)
		}

		return nil
	}, parsed.FilePattern)
	if err != nil && !truncated {
		return ToolError(TextBlocks(fmt.Sprintf("Error: grep walk failed: %v", err))), nil
	}

	// Render in deterministic order: sorted by path, then line number.
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].lineNum < out[j].lineNum
	})

	var sb strings.Builder
	renderedSize := 0
	for _, l := range out {
		prefix := ""
		if l.isMatch {
			prefix = ">"
		}
		lineOut := fmt.Sprintf("%s%s:%d:%s\n", prefix, l.path, l.lineNum, l.content)
		// Defensive re-check on the final (sorted) output order so the cap
		// holds regardless of walk order.
		if renderedSize+len(lineOut) > maxGrepOutputBytes && sb.Len() > 0 {
			truncated = true
			break
		}
		renderedSize += len(lineOut)
		sb.WriteString(lineOut)
	}

	if contextN > 0 {
		output := sb.String()
		outputLines := strings.Split(output, "\n")
		sb.Reset()
		var prev string
		for _, line := range outputLines {
			if line != prev && line != "" {
				sb.WriteString(line + "\n")
			}
			prev = line
		}
	}

	output := sb.String()
	if truncated {
		output += "... (output truncated at 2 KiB)"
	}

	return Success(TextBlocks(output)), nil
}

// Ensure GrepTool implements ToolHandler at compile time.
var _ ToolHandler = (*GrepTool)(nil)
