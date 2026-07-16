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
	Path    string `json:"path" jsonschema:"File path relative to workspace root, or an absolute path within the workspace."`
	Content string `json:"content" jsonschema:"File content as UTF-8 text. For new files, parent directories are created automatically."`
}

// WriteTool implements ToolHandler for creating and overwriting files.
type WriteTool struct {
	workspace string
	schema    litellm.Schema
}

// NewWriteTool creates a new WriteTool.
func NewWriteTool(workspace string) *WriteTool {
	return &WriteTool{
		workspace: workspace,
		schema:    SchemaOf[writeArgs](),
	}
}

func (t *WriteTool) Name() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Create a brand new file, or completely overwrite an existing file. Creates parent directories automatically. Returns bytes written and directories created. Do not use for minor changes — use the edit tool for precise search-and-replace modifications instead."
}

func (t *WriteTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *WriteTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed writeArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("write: invalid args: %w", err), false
	}

	if parsed.Path == "" {
		return textBlocks("Error: path is required"), nil, true
	}

	if parsed.Content == "" {
		return textBlocks("Error: content is required"), nil, true
	}

	absPath, err := fileutil.ValidateWorkspacePath(parsed.Path, t.workspace)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return textBlocks(fmt.Sprintf("Error: creating directories: %v", err)), nil, true
	}

	// Check if file already exists (for output messaging)
	exists := false
	if _, err := os.Stat(absPath); err == nil {
		exists = true
	}

	// Write file
	if err := os.WriteFile(absPath, []byte(parsed.Content), 0o644); err != nil {
		return textBlocks(fmt.Sprintf("Error: writing file: %v", err)), nil, true
	}

	bytesWritten := len(parsed.Content)

	var output string
	if exists {
		output = fmt.Sprintf("Wrote %d bytes to %s (overwrite)", bytesWritten, parsed.Path)
	} else {
		output = fmt.Sprintf("Wrote %d bytes to %s (new file)", bytesWritten, parsed.Path)
	}

	return textBlocks(output), nil, false
}
