package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type writeArgs struct {
	Path    string  `json:"path" jsonschema:"File path relative to workspace root, or an absolute path within the workspace, or an absolute path within any configured writable root (sandbox.extra_writable_paths). A /tmp/... target maps to the run's session sandbox tmpdir (host /tmp when not sandboxed)."`
	Content *string `json:"content" jsonschema:"File content as UTF-8 text. For new files, parent directories are created automatically."`
}

// WriteTool implements ToolHandler for creating and overwriting files.
type WriteTool struct {
	workspace     string
	writableRoots []string
	tmpdirFor     fileutil.TmpdirFor
	schema        litellm.Schema
}

// NewWriteTool creates a new WriteTool.
// writableRoots may be nil — behavior is workspace-only validation, with
// /tmp targets rewritten to the session sandbox tmpdir when tmpdirFor tracks
// one (ADR-0026). tmpdirFor may be nil (sandbox none / bwrap unavailable) —
// /tmp targets then pass through unchanged.
func NewWriteTool(workspace string, writableRoots []string, tmpdirFor fileutil.TmpdirFor) *WriteTool {
	return &WriteTool{
		workspace:     workspace,
		writableRoots: writableRoots,
		tmpdirFor:     tmpdirFor,
		schema:        SchemaOf[writeArgs](),
	}
}

func (t *WriteTool) Name() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Create or overwrite a file. Creates parent dirs automatically. Returns bytes written and dirs created. For minor changes use edit instead."
}

func (t *WriteTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *WriteTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed writeArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("write: invalid args: %w", err)
	}

	if parsed.Path == "" {
		return ToolError(TextBlocks("Error: path is required")), nil
	}

	if parsed.Content == nil {
		return ToolError(TextBlocks("Error: content is required")), nil
	}

	sessionID, _ := ctx.Value(SessionIDKey).(string)
	absPath, err := fileutil.ResolveWritablePath(parsed.Path, t.workspace, t.writableRoots, sessionID, t.tmpdirFor)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: %v", err))), nil
	}

	// Ensure parent directory exists and track how many were created
	dir := filepath.Dir(absPath)
	dirsCreated := countNewDirs(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: creating directories: %v", err))), nil
	}

	// Check if file already exists (for output messaging)
	exists := false
	if _, err := os.Stat(absPath); err == nil {
		exists = true
	}

	// Write file
	content := *parsed.Content
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: writing file: %v", err))), nil
	}

	bytesWritten := len(content)

	var output string
	if exists {
		output = fmt.Sprintf("Wrote %d bytes to %s (overwrite)", bytesWritten, parsed.Path)
	} else {
		output = fmt.Sprintf("Wrote %d bytes to %s (new file)", bytesWritten, parsed.Path)
	}

	if dirsCreated > 0 {
		output = fmt.Sprintf("%s, %d dirs created", output, dirsCreated)
	}

	return Success(TextBlocks(output)), nil
}

// countNewDirs returns how many directories must be created for a given path.
// It walks up from path to find the first existing parent, then counts
// components from that parent down to path.
func countNewDirs(path string) int {
	// If path already exists, nothing to create
	if _, err := os.Stat(path); err == nil {
		return 0
	}

	// Walk up to find first existing ancestor.
	// Track components traversed from original path upward.
	steps := 0
	current := path
	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Hit root — all path components are new.
			// steps already counts them all.
			return steps
		}
		steps++
		if _, err := os.Stat(parent); err == nil {
			// Found existing ancestor — steps is count of new dirs
			return steps
		}
		current = parent
	}
}
