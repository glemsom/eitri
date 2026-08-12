package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// editTool replaces the first unique occurrence of old_string with new_string
// in an existing file. It is host-side and validates the target against the
// writable roots like write.
type editTool struct {
	val *Validator
}

func (e *editTool) Name() string {
	return "edit"
}

func (e *editTool) Description() string {
	return "Edit an existing file by replacing the first unique occurrence of old_string with new_string. Errors if old_string is absent or appears more than once. Target must be inside a writable root."
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

func (e *editTool) Run(ctx context.Context, args map[string]any) (string, error) {
	path, err := strArg(args, "path")
	if err != nil {
		return "", err
	}
	old, err := strArg(args, "old_string")
	if err != nil {
		return "", err
	}
	newStr, err := optStr(args, "new_string")
	if err != nil {
		return "", err
	}
	host, err := e.val.Resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(host)
	if err != nil {
		return "", fmt.Errorf("edit %s: %w", path, err)
	}
	text := string(data)
	count := strings.Count(text, old)
	switch count {
	case 0:
		return "", fmt.Errorf("edit %s: old_string not found", path)
	case 1:
		// fall through
	default:
		return "", fmt.Errorf("edit %s: old_string matched %d times; make it unique", path, count)
	}
	updated := strings.Replace(text, old, newStr, 1)
	if err := os.WriteFile(host, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit %s: %w", path, err)
	}
	return fmt.Sprintf("Edit applied to %s", path), nil
}
