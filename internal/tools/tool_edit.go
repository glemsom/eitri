package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	return "Replace old_string with new_string in an existing file. old_string must occur exactly once; zero or multiple matches is a hard error, no silent partial application. If the exact match fails, a whitespace-tolerant fallback retries with per-line whitespace normalized and still requires a unique match. If old_string appears more than once, widen it with unique surrounding context (enclosing function signature, neighbouring line). Base old_string on a fresh read, not remembered content. The file must exist; path must be inside a writable root."
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
	old, err := optStr(args, "old_string")
	if err != nil {
		return ToolResult{}, err
	}
	newStr, err := optStr(args, "new_string")
	if err != nil {
		return ToolResult{}, err
	}
	// Guards before any file access so a degenerate call fails fast instead
	// of surfacing as a confusing match-count or not-found error.
	if old == "" {
		return ToolResult{}, fmt.Errorf("edit: old_string must not be empty")
	}
	if old == newStr {
		return ToolResult{}, fmt.Errorf("edit %s: old_string equals new_string; no-op edit", path)
	}
	host, err := e.val.Resolve(path)
	if err != nil {
		return ToolResult{}, err
	}
	// A rename over a symlink replaces the link itself; follow it first so
	// edits land in the target file as before, and re-validate so following
	// the link cannot escape the writable roots.
	if resolved, linkErr := filepath.EvalSymlinks(host); linkErr == nil && resolved != host {
		host, err = e.val.Resolve(resolved)
		if err != nil {
			return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
		}
	}
	info, err := os.Stat(host)
	if err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	data, err := os.ReadFile(host)
	if err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	text := string(data)
	count := strings.Count(text, old)
	var updated string
	switch {
	case count == 1:
		updated = strings.Replace(text, old, newStr, 1)
	default:
		u, ok := normalizedFallback(text, old, newStr)
		if !ok {
			if count == 0 {
				return ToolResult{}, fmt.Errorf("edit %s: old_string not found", path)
			}
			return ToolResult{}, fmt.Errorf("edit %s: old_string matched %d times; make it unique", path, count)
		}
		updated = u
	}
	// Atomic write: stage the new content in a same-directory temp file, then
	// rename it over the target. A crash mid-edit therefore leaves either the
	// old or the new content, never a truncated mix. Same directory keeps the
	// rename on one filesystem; preserving the original mode avoids the mode
	// reset a fresh temp file would cause.
	dir := filepath.Dir(host)
	tmp, err := os.CreateTemp(dir, ".eitri-edit-*")
	if err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write([]byte(updated)); err != nil {
		tmp.Close()
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	// Sync before the rename so the crash window closes on content, not just
	// on the directory entry.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	if err := os.Rename(tmpName, host); err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", path, err)
	}
	return ToolResult{Text: fmt.Sprintf("Edit applied to %s", path)}, nil
}

// normalized collapses leading/trailing and internal whitespace runs to
// single spaces so drifted indentation or alignment still matches.
func normalized(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizedFallback retries the match with each line's whitespace normalized
// sides of each line; it returns the updated file content when exactly one region matches,
// and "" otherwise (zero or multiple matches), preserving the strict unique
// match guarantee. The replacement keeps the file's own line endings and
// trailing-newline state.
func normalizedFallback(text, old, newStr string) (string, bool) {
	crlf := strings.Contains(text, "\r\n")
	if crlf {
		// Work in LF space so matching and reconstruction see clean lines;
		// converted back below before returning.
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	textLines := splitLines(text)
	oldLines := splitLines(strings.TrimSuffix(old, "\n"))
	if len(oldLines) == 0 || len(oldLines) > len(textLines) {
		return "", false
	}
	matches := []int{}
	for i := 0; i+len(oldLines) <= len(textLines); i++ {
		ok := true
		for j := range oldLines {
			if normalized(textLines[i+j]) != normalized(oldLines[j]) {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, i)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	newLines := splitLines(strings.TrimSuffix(newStr, "\n"))
	out := make([]string, 0, len(textLines)-len(oldLines)+len(newLines))
	out = append(out, textLines[:matches[0]]...)
	out = append(out, newLines...)
	out = append(out, textLines[matches[0]+len(oldLines):]...)
	joined := strings.Join(out, "\n")
	if strings.HasSuffix(text, "\n") && !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	// Reconstruction joins on \n; a CRLF file gets its endings restored here.
	if crlf {
		joined = strings.ReplaceAll(joined, "\n", "\r\n")
	}
	return joined, true
}
