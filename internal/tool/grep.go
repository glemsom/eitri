package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/voocel/litellm"
)

const maxGrepOutput = 128 * 1024 // 128 KiB

type grepArgs struct {
	Pattern     string  `json:"pattern" jsonschema:"Regular expression pattern to search for (Go RE2 syntax)."`
	FilePattern *string `json:"file_pattern,omitempty" jsonschema:"Optional glob pattern to filter files by name (e.g. '*.go')."`
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
	return "Search file contents using a regular expression pattern (Go RE2 syntax). Results are returned as file:line:content, sorted by file then line number. Optionally filter by file_glob pattern (e.g. '*.go'). Output is capped at 128 KiB."
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
		return textBlocks(fmt.Sprintf("Error: invalid regex: %v", err)), nil, true
	}

	type match struct {
		path    string
		lineNum int
		content string
	}

	var matches []match
	var buf bytes.Buffer

	err = filepath.WalkDir(t.workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}

		// Skip vendor directory
		if d.IsDir() && d.Name() == "vendor" {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		// Apply file_pattern filter if set
		if parsed.FilePattern != nil {
			matched, err := filepath.Match(*parsed.FilePattern, d.Name())
			if err != nil || !matched {
				return nil
			}
		}

		// Get relative path from workspace
		relPath, err := filepath.Rel(t.workspace, path)
		if err != nil {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		// Check if file is binary (look for null bytes)
		if bytes.Contains(data, []byte{0}) {
			return nil // skip binary files
		}

		lines := bytes.Split(data, []byte("\n"))
		for i, line := range lines {
			if re.Match(line) {
				matches = append(matches, match{
					path:    relPath,
					lineNum: i + 1,
					content: string(line),
				})
			}
		}

		return nil
	})
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: grep walk failed: %v", err)), nil, true
	}

	// Sort by file path then line number
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	// Build output, capped at maxGrepOutput
	truncated := false
	for _, m := range matches {
		line := fmt.Sprintf("%s:%d:%s\n", m.path, m.lineNum, m.content)
		if buf.Len()+len(line) > maxGrepOutput {
			truncated = true
			break
		}
		buf.WriteString(line)
	}

	result := strings.TrimSuffix(buf.String(), "\n")
	if truncated {
		result += "\n... truncated (output exceeds 128 KiB limit)"
	}

	return textBlocks(result), nil, false
}
