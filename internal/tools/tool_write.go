package tools

import (
	"context"
	"fmt"
	"os"
)

// writeTool writes a whole file, creating or overwriting it.
type writeTool struct {
	val *Validator
}

func (w *writeTool) Name() string {
	return "write"
}

func (w *writeTool) Description() string {
	return "Write the entire content to a file, creating it if absent and overwriting it if present. Target must be inside the workspace, session temp, or an extra writable path."
}

func (w *writeTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "The file path to write (workspace, session temp, or extra writable path).",
		},
		"content": map[string]any{
			"type":        "string",
			"description": "The full file content.",
		},
	}, []string{"path", "content"})
}

func (w *writeTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	path, err := strArg(args, "path")
	if err != nil {
		return ToolResult{}, err
	}
	content, err := strArg(args, "content")
	if err != nil {
		return ToolResult{}, err
	}
	host, err := w.val.Resolve(path)
	if err != nil {
		return ToolResult{}, err
	}
	if err := os.WriteFile(host, []byte(content), 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("write %s: %w", path, err)
	}
	return ToolResult{Text: fmt.Sprintf("Wrote %d bytes to %s", len(content), path)}, nil
}
