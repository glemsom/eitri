package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// readTool reads a file by explicit line range so the agent need not dump whole files into context.
type readTool struct {
	tr        *PathTranslator
	workspace string
}

func (r *readTool) Name() string {
	return "read"
}

func (r *readTool) Description() string {
	return "Read a file. Omitted or null start_line/end_line read the ENTIRE file (start_line defaults to 1, end_line to EOF) — no error, a full dump into context. Explicit 1-based line ranges are the cheap path: prefer them for large files. Output is line-numbered; oversized results are truncated at the tool-result boundary with an explicit \"+N more\" marker (never silent). Paths resolve in the shared path namespace."
}

func (r *readTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "The file path to read: a workspace path, a sandbox /tmp path, or any host path the sandbox can read (read is not restricted to writable roots).",
		},
		"start_line": map[string]any{"type": "integer"},
		"end_line":   map[string]any{"type": "integer"},
	}, []string{"path"})
}

func (r *readTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	path, err := strArg(args, "path")
	if err != nil {
		return ToolResult{}, err
	}
	start, err := argInt(args, "start_line", 1)
	if err != nil {
		return ToolResult{}, err
	}
	end, err := argInt(args, "end_line", 0) // 0 = to EOF
	if err != nil {
		return ToolResult{}, err
	}
	host := r.tr.Resolve(path, r.workspace)
	data, err := os.ReadFile(host)
	if err != nil {
		return ToolResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(data)
	lines := splitLines(text)
	if start < 1 {
		start = 1
	}
	if end == 0 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return ToolResult{}, fmt.Errorf("start_line %d beyond file length %d", start, len(lines))
	}
	if start > end {
		return ToolResult{}, fmt.Errorf("start_line %d after end_line %d", start, end)
	}
	var b []byte
	for i := start; i <= end; i++ {
		b = append(b, fmt.Appendf(nil, "%6d\t%s\n", i, lines[i-1])...)
	}
	return ToolResult{Text: string(b)}, nil
}

func argInt(args map[string]any, key string, def int) (int, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return def, nil
	}
	var out int
	switch n := v.(type) {
	case int:
		out = n
	case float64:
		out = int(n)
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("argument %q must be an integer", key)
		}
		out = i
	default:
		return 0, fmt.Errorf("argument %q must be an integer", key)
	}
	return out, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
