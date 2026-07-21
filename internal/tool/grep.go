package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

const maxGrepOutputBytes = 2 * 1024

type grepArgs struct {
	Pattern     string `json:"pattern" jsonschema:"Go regex (RE2 syntax) pattern to search for in file contents."`
	FilePattern string `json:"file_pattern,omitempty" jsonschema:"Optional glob pattern to filter files by path relative to workspace root (e.g. '*.go' to search only Go files)."`

	Context int `json:"context,omitempty" jsonschema:"Number of surrounding context lines to include before and after each match (default 0)."`
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

func (t *GrepTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("grep: invalid args: %w", err), false
	}

	if parsed.Pattern == "" {
		return textBlocks("Error: pattern is required"), nil, true
	}

	re, err := regexp.Compile(parsed.Pattern)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: invalid regex %q: %v", parsed.Pattern, err)), nil, true
	}

	type match struct {
		path    string
		lineNum int
		content string
	}

	var matches []match
	outputSize := 0
	truncated := false
	contextN := parsed.Context
	if contextN < 0 {
		contextN = 0
	}

	// fileCache stores all lines per file for context extraction
	type fileLines struct {
		relPath string
		lines   []string
	}
	var fileCache []fileLines

	err = fileutil.WalkWorkspace(t.workspace, func(path, relPath string, d os.DirEntry) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.ContainsRune(string(data), '\x00') {
			return nil
		}

		allLines := strings.Split(string(data), "\n")

		if contextN > 0 {
			fileCache = append(fileCache, fileLines{relPath: relPath, lines: allLines})
		}

		for lineNum, line := range allLines {
			lineNum++ // 1-indexed
			if re.MatchString(line) {
				prefix := ""
				if contextN > 0 {
					prefix = ">"
				}
				lineSize := len(prefix) + len(relPath) + 1 + len(fmt.Sprintf("%d", lineNum)) + 1 + len(line) + 1
				if outputSize+lineSize > maxGrepOutputBytes && len(matches) > 0 {
					truncated = true
					return &fileutil.WalkStop{}
				}
				matches = append(matches, match{path: relPath, lineNum: lineNum, content: line})
				outputSize += lineSize
			}
		}

		return nil
	}, parsed.FilePattern)
	if err != nil && !truncated {
		return textBlocks(fmt.Sprintf("Error: grep walk failed: %v", err)), nil, true
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	var sb strings.Builder

	if contextN == 0 || len(matches) == 0 {
		for _, m := range matches {
			sb.WriteString(fmt.Sprintf("%s:%d:%s\n", m.path, m.lineNum, m.content))
		}
	} else {
		cacheMap := make(map[string][]string)
		for _, fl := range fileCache {
			cacheMap[fl.relPath] = fl.lines
		}

		var lastPath string
		var lastLine int
		for _, m := range matches {
			lines, ok := cacheMap[m.path]
			if !ok {
				sb.WriteString(fmt.Sprintf(">%s:%d:%s\n", m.path, m.lineNum, m.content))
				continue
			}

			start := m.lineNum - 1 - contextN
			end := m.lineNum - 1 + contextN
			if start < 0 {
				start = 0
			}
			if end >= len(lines) {
				end = len(lines) - 1
			}
			if m.path == lastPath && start < lastLine {
				start = lastLine
			}

			for i := start; i <= end; i++ {
				lineNum := i + 1
				var lineOut string
				if i == m.lineNum-1 {
					lineOut = fmt.Sprintf(">%s:%d:%s\n", m.path, lineNum, lines[i])
				} else {
					lineOut = fmt.Sprintf("%s:%d:%s\n", m.path, lineNum, lines[i])
				}
				if outputSize+len(lineOut) > maxGrepOutputBytes {
					truncated = true
					goto done
				}
				sb.WriteString(lineOut)
				outputSize += len(lineOut)
				lastLine = lineNum
			}
		}
	done:
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

	return textBlocks(output), nil, false
}

// Ensure GrepTool implements ToolHandler at compile time.
var _ ToolHandler = (*GrepTool)(nil)
