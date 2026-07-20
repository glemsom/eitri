package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type globArgs struct {
	Pattern string `json:"pattern" jsonschema:"Glob pattern to match files against, relative to workspace root. Uses Go filepath.Match syntax (no ** support)."`
}

// GlobTool implements ToolHandler for finding files by glob pattern.
type GlobTool struct {
	workspace string
	schema    litellm.Schema
}

// NewGlobTool creates a new GlobTool.
func NewGlobTool(workspace string) *GlobTool {
	return &GlobTool{
		workspace: workspace,
		schema:    SchemaOf[globArgs](),
	}
}

func (t *GlobTool) Name() string {
	return "glob"
}

func (t *GlobTool) Description() string {
	return "Find files by glob pattern relative to workspace root. Returns sorted unique paths, one per line. Excludes .hidden and vendor/ dirs."
}

func (t *GlobTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *GlobTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed globArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("glob: invalid args: %w", err), false
	}

	if parsed.Pattern == "" {
		return textBlocks("Error: pattern is required"), nil, true
	}

	// Validate workspace path
	absPattern, err := fileutil.ValidateWorkspacePath(parsed.Pattern, t.workspace)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	// Walk the workspace looking for matches
	var matches []string
	err = fileutil.WalkWorkspace(t.workspace, func(path, relPath string, d os.DirEntry) error {
		// Match pattern against just the filename first (so *.go matches sub/qux.go)
		matched, err := filepath.Match(parsed.Pattern, filepath.Base(relPath))
		if err != nil {
			matched = false
		}

		// If filename-only match fails, try against full relative path
		// (for path-prefixed patterns like internal/tool/*.go)
		if !matched {
			matched, err = filepath.Match(parsed.Pattern, relPath)
			if err != nil {
				matched = false
			}
		}

		// Also try against absolute path (for patterns resolved by ValidateWorkspacePath)
		if !matched {
			matched, err = filepath.Match(absPattern, path)
			if err != nil {
				matched = false
			}
		}

		if matched {
			matches = append(matches, relPath)
		}

		return nil
	}, "")
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: glob walk failed: %v", err)), nil, true
	}

	// Sort and deduplicate
	sort.Strings(matches)
	matches = unique(matches)

	return textBlocks(strings.Join(matches, "\n")), nil, false
}

// unique returns the input slice with duplicates removed.
// The input must already be sorted.
func unique(s []string) []string {
	if len(s) == 0 {
		return s
	}
	result := make([]string, 0, len(s))
	result = append(result, s[0])
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1] {
			result = append(result, s[i])
		}
	}
	return result
}
