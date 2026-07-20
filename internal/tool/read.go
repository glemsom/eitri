package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type readArgs struct {
	Path      string `json:"path" jsonschema:"File path relative to workspace root, or an absolute path within the workspace."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"1-indexed line number to start reading from (default: 1)."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"1-indexed line number to stop reading at (default: 100)."`
}

// ReadTool implements ToolHandler for reading files with line-range support.
type ReadTool struct {
	workspace    string
	skillDirs    []string
	allowedPaths []string
	schema       litellm.Schema
}

// NewReadTool creates a new ReadTool.
// allowedPaths may be nil — behavior is workspace-only validation.
func NewReadTool(workspace string, skillDirs []string, allowedPaths ...[]string) *ReadTool {
	var ap []string
	if len(allowedPaths) > 0 {
		ap = allowedPaths[0]
	}
	return &ReadTool{
		workspace:    workspace,
		skillDirs:    skillDirs,
		allowedPaths: ap,
		schema:       SchemaOf[readArgs](),
	}
}

func (t *ReadTool) Name() string {
	return "read"
}

// AppendAllowedPaths adds one or more paths to the temporary allowed paths
// for this ReadTool. Used by the agent loop when a confirmation is approved
// so the tool can re-execute without requiring another confirmation.
func (t *ReadTool) AppendAllowedPaths(paths ...string) {
	t.allowedPaths = append(t.allowedPaths, paths...)
}

func (t *ReadTool) Description() string {
	return "Read a file from workspace. Use start_line/end_line from grep output. Defaults to lines 1-100. Metadata prefix shows total lines when truncated; continue reading with adjusted range."
}

func (t *ReadTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *ReadTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed readArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("read: invalid args: %w", err), false
	}

	if parsed.Path == "" {
		return textBlocks("Error: path is required"), nil, true
	}

	// Validate path against workspace, skill dirs, and allowed paths
	allowedRoots := append([]string{}, t.skillDirs...)
	allowedRoots = append(allowedRoots, t.allowedPaths...)
	absPath, err := fileutil.ValidatePathWithAllowed(parsed.Path, t.workspace, allowedRoots)
	if err != nil {
		return nil, &ErrNeedsConfirmation{
			Path:    parsed.Path,
			Message: fmt.Sprintf("path %q is outside workspace and allowed read paths: %v", parsed.Path, err),
		}, false
	}

	startLine := parsed.StartLine
	if startLine < 1 {
		startLine = 1
	}

	endLine := parsed.EndLine
	if endLine < 1 {
		endLine = 100
	}
	if endLine < startLine {
		endLine = startLine
	}

	limit := endLine - startLine + 1

	vr, err := fileutil.ReadFile(absPath, startLine, limit)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	output := vr.Content
	if vr.Truncated {
		output = fmt.Sprintf("[file: %s, lines %d-%d of %d]\n%s", parsed.Path, startLine, startLine+limit-1, vr.TotalLines, vr.Content)
	}

	return textBlocks(output), nil, false
}
