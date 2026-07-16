package tool

import (
	"bufio"
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

const maxGrepOutputBytes = 128 * 1024

type grepArgs struct {
	Pattern     string `json:"pattern" jsonschema:"Go regex (RE2 syntax) pattern to search for in file contents."`
	FilePattern string `json:"file_pattern,omitempty" jsonschema:"Optional glob pattern to filter files by path relative to workspace root (e.g. '*.go' to search only Go files)."`
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
	return "Search file contents using regex (RE2 syntax). Optionally filter by file pattern (glob). Results are returned as file:line:content, sorted by file then line number. Output is capped at 128 KiB."
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

	// Compile regex
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

	err = fileutil.WalkWorkspace(t.workspace, func(path, relPath string, d os.DirEntry) error {
		// Check if text file (skip binary)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.ContainsRune(string(data), '\x00') {
			return nil
		}

		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				// Estimate output size: "path:line:content\n"
				lineSize := len(relPath) + 1 + len(fmt.Sprintf("%d", lineNum)) + 1 + len(line) + 1
				if outputSize+lineSize > maxGrepOutputBytes && len(matches) > 0 {
					truncated = true
					return &fileutil.WalkStop{} // stop the entire walk
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

	// Sort by file then line number
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	// Build output
	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(fmt.Sprintf("%s:%d:%s\n", m.path, m.lineNum, m.content))
	}

	output := sb.String()
	if truncated {
		output += "... (output truncated at 128 KiB)"
	}

	return textBlocks(output), nil, false
}

// Ensure GrepTool implements ToolHandler at compile time.
var _ ToolHandler = (*GrepTool)(nil)
