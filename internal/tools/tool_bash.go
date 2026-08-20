package tools

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/compress"
)

// bashTool runs a shell command inside the bwrap sandbox with host network, returning the combined stdout+stderr (token-efficient single stream).
type bashTool struct {
	sb *Sandbox
}

func (b *bashTool) Name() string {
	return "bash"
}

func (b *bashTool) Description() string {
	return "Execute a shell command inside the bwrap sandbox. The workspace is writable, /tmp is the session temp, root is read-only, /proc is a fresh pid-namespace-scoped procfs, /dev is a private devtmpfs with a writable /dev/shm, and the command has host network access. Returns combined stdout+stderr as one stream. Output passes through a deterministic compressor at the tool-result boundary: ANSI escape sequences are stripped, repeated consecutive lines are collapsed, and progress/redraw frames are collapsed. Listings longer than a bounded line budget are truncated with an explicit \"+N more\" marker — never silent — so treat a truncated listing as partial; re-running the command is the recovery path if you need the tail. The compression is deterministic: running the same command again yields the same compressed form. Terse output passes through untouched."
}

func (b *bashTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"command": map[string]any{
			"type":        "string",
			"description": "The shell command to run, executed by /bin/bash -c.",
		},
	}, []string{"command"})
}

func (b *bashTool) Run(ctx context.Context, args map[string]any) (ToolResult, error) {
	cmd, err := strArg(args, "command")
	if err != nil {
		return ToolResult{}, err
	}
	o, err := b.sb.Run(ctx, cmd)
	if err != nil {
		return ToolResult{Text: o.Combined()}, err
	}
	text, compressed := compress.CompressResult(o.Combined())
	return ToolResult{Text: text, Compressed: compressed}, nil
}

// Combined returns stdout then stderr joined, prioritizing stdout for token efficiency while keeping stderr visible.
func (o *Output) Combined() string {
	switch {
	case o.Stdout != "" && o.Stderr != "":
		return strings.TrimSuffix(o.Stdout, "\n") + "\n" + o.Stderr
	case o.Stderr != "":
		return o.Stderr
	default:
		return o.Stdout
	}
}
