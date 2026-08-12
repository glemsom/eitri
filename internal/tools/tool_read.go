package tools

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// readTool reads a file by explicit line range so the agent need not dump
// whole files into context. It is host-side (outside the cage) but resolves
// the shared path namespace via the validator.
type readTool struct {
	val *Validator
}

func (r *readTool) Name() string {
	return "read"
}

func (r *readTool) Description() string {
	return "Read a file, optionally limited to a line range (start_line..end_line, 1-based). Returns the requested lines with content; paths resolve in the shared path namespace."
}

func (r *readTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "The file path, in the shared path namespace (workspace or sandbox /tmp).",
		},
		"start_line": map[string]any{
			"type":        "integer",
			"description": "Optional 1-based start line (default 1).",
		},
		"end_line": map[string]any{
			"type":        "integer",
			"description": "Optional 1-based end line, inclusive (default: through EOF).",
		},
	}, []string{"path"})
}

func (r *readTool) Run(ctx context.Context, args map[string]any) (string, error) {
	path, err := strArg(args, "path")
	if err != nil {
		return "", err
	}
	start, err := argInt(args, "start_line", 1)
	if err != nil {
		return "", err
	}
	end, err := argInt(args, "end_line", 0) // 0 = to EOF
	if err != nil {
		return "", err
	}
	host, err := r.val.Resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(host)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
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
		return "", fmt.Errorf("start_line %d beyond file length %d", start, len(lines))
	}
	if start > end {
		return "", fmt.Errorf("start_line %d after end_line %d", start, end)
	}
	var b []byte
	for i := start; i <= end; i++ {
		b = append(b, []byte(fmt.Sprintf("%6d\t%s\n", i, lines[i-1]))...)
	}
	return string(b), nil
}

func argInt(args map[string]any, key string, def int) (int, error) {
	v, ok := args[key]
	if !ok {
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
