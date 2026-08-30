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
	return "Execute a shell command in a sandbox. Returns the combined stream (stdout then stderr; ANSI escape sequences stripped, repeated consecutive lines collapsed). Output passes through a deterministic line compressor: heavy listings are truncated with an explicit \"+N more\" marker — never silent — so re-running the command is the recovery path if you need the tail. Same command yields the same compressed form."
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
		if o == nil {
			return ToolResult{}, err
		}
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
