package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// editTool replaces a uniquely-occurring old_string with new_string in an existing file.
type editTool struct {
	val *Validator
}

func (e *editTool) Name() string {
	return "edit"
}

func (e *editTool) Description() string {
	return "Edit an existing file by replacing old_string with new_string. old_string must match the file content EXACTLY (whitespace, indentation, and escaping included) and must occur EXACTLY once; matching zero times or more than once is a hard error, with no silent partial application. When the target text appears multiple times, widen old_string to include unique surrounding context (e.g. the enclosing function signature or a neighbouring line) so the match becomes unique. Base old_string on a FRESH read of the file, not remembered content, so drift between believed and on-disk content cannot cause a spurious not-found error. The file must already exist and the target must be inside a writable root (workspace, session temp, or extra writable path)."
}

func (e *editTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "The file path to edit (workspace or session temp / extra writable path).",
		},
		"old_string": map[string]any{
			"type":        "string",
			"description": "Text to replace; must match exactly once in the file.",
		},
		"new_string": map[string]any{
			"type":        "string",
			"description": "Replacement text.",
		},
	}, []string{"path", "old_string", "new_string"})
}

func (e *editTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	path, err := strArg(args, "path")
	if err != nil {
		return ToolResult{}, err
	}
	old, err := strArg(args, "old_string")
	if err != nil {
		return ToolResult{}, err
	}
	newStr, err := optStr(args, "new_string")
	if err != nil {
		return ToolResult{}, err
	}
	host, err := e.val.Resolve(path)
	if err != nil {
		return ToolResult{}, err
	}
	data, err := os.ReadFile(host)
	if err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	text := string(data)
	count := strings.Count(text, old)
	switch count {
	case 0:
		return ToolResult{}, fmt.Errorf("edit %s: old_string not found", path)
	case 1:
	default:
		return ToolResult{}, fmt.Errorf("edit %s: old_string matched %d times; make it unique", path, count)
	}
	updated := strings.Replace(text, old, newStr, 1)
	if err := os.WriteFile(host, []byte(updated), 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	return ToolResult{Text: fmt.Sprintf("Edit applied to %s", path)}, nil
}
