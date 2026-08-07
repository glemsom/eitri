package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type editArgs struct {
	Path    string `json:"path" jsonschema:"File path relative to workspace root, or an absolute path within the workspace, or an absolute path within any configured writable root (sandbox.extra_writable_paths). A /tmp/... target maps to the run's session sandbox tmpdir (host /tmp when not sandboxed)."`
	OldText string `json:"old_text" jsonschema:"Exact text block to find (use surrounding lines as context anchors for uniqueness)"`
	NewText string `json:"new_text" jsonschema:"Replacement text for the matched block"`
}

// EditTool implements ToolHandler for precise search-and-replace on existing files.
type EditTool struct {
	workspace     string
	writableRoots []string
	tmpdirFor     fileutil.TmpdirFor
	schema        litellm.Schema
}

// NewEditTool creates a new EditTool.
// writableRoots may be nil — behavior is workspace-only validation, with
// /tmp targets rewritten to the session sandbox tmpdir when tmpdirFor tracks
// one (ADR-0026). tmpdirFor may be nil (sandbox none / bwrap unavailable) —
// /tmp targets then pass through unchanged.
func NewEditTool(workspace string, writableRoots []string, tmpdirFor fileutil.TmpdirFor) *EditTool {
	return &EditTool{
		workspace:     workspace,
		writableRoots: writableRoots,
		tmpdirFor:     tmpdirFor,
		schema:        SchemaOf[editArgs](),
	}
}

func (t *EditTool) Name() string {
	return "edit"
}

func (t *EditTool) Description() string {
	return "Precise search-and-replace on an existing file. old_text must uniquely match one location. Always include surrounding context lines for uniqueness. Shows diff in UI. For new files use write instead."
}

func (t *EditTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *EditTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed editArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("edit: invalid args: %w", err)
	}

	if parsed.Path == "" {
		return ToolError(TextBlocks("Error: path is required")), nil
	}

	sessionID, _ := ctx.Value(SessionIDKey).(string)
	absPath, err := fileutil.ResolveWritablePath(parsed.Path, t.workspace, t.writableRoots, sessionID, t.tmpdirFor)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: %v", err))), nil
	}

	// Read file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: cannot read file: %v", err))), nil
	}

	oldContent := string(data)

	// Count matches
	count := strings.Count(oldContent, parsed.OldText)
	if count == 0 {
		// Provide nearby content hint: show first lines so LLM can self-correct
		lines := strings.SplitN(oldContent, "\n", 6)
		trunc := lines
		if len(lines) > 5 {
			trunc = lines[:5]
		}
		prefix := strings.Join(trunc, "\n")
		if len(lines) > 5 {
			prefix += "..."
		}
		return ToolError(TextBlocks(fmt.Sprintf("Error: text %q not found in file. File starts with:\n%s", parsed.OldText, prefix))), nil
	}
	if count > 1 {
		return ToolError(TextBlocks(fmt.Sprintf("Error: text %q matched %d times in file, expected exactly 1 match. Include more surrounding context in 'old_text' to make it unique.", parsed.OldText, count))), nil
	}

	// Perform replacement
	newContent := strings.Replace(oldContent, parsed.OldText, parsed.NewText, 1)

	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: failed to write file: %v", err))), nil
	}

	// Count lines changed
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	lineChanges := countLineDiffs(oldLines, newLines)

	// Build summary with correct pluralization
	lineWord := "lines"
	if lineChanges == 1 {
		lineWord = "line"
	}
	return TextResult(fmt.Sprintf("Edited file: %s (%d %s changed)", parsed.Path, lineChanges, lineWord)), nil
}

// countLineDiffs returns the number of lines that differ between two line slices.
func countLineDiffs(oldLines, newLines []string) int {
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	diffs := 0
	for i := 0; i < maxLen; i++ {
		var o, n string
		if i < len(oldLines) {
			o = oldLines[i]
		}
		if i < len(newLines) {
			n = newLines[i]
		}
		if o != n {
			diffs++
		}
	}
	return diffs
}
