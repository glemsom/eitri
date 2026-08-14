package tools

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/compress"
)

// bashTool runs a shell command inside the bwrap sandbox with host network,
// returning the combined stdout+stderr (token-efficient single stream). Its
// output is routed through the deterministic tool-output compressor so noisy
// `ls`/`find`/`grep`/`rg` reads stay cheap (docs/spec.md §5); the never-inflate
// gate keeps terse output untouched, and recovery is a re-run of the command.
type bashTool struct {
	sb *Sandbox
}

func (b *bashTool) Name() string {
	return "bash"
}

func (b *bashTool) Description() string {
	return "Execute a shell command inside the bwrap sandbox. The workspace is writable, /tmp is the session temp, root is read-only, /proc is a fresh pid-namespace-scoped procfs, /dev is a private devtmpfs with a writable /dev/shm, and the command has host network access. Returns combined stdout+stderr."
}

func (b *bashTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"command": map[string]any{
			"type":        "string",
			"description": "The shell command to run, executed by /bin/bash -c.",
		},
	}, []string{"command"})
}

func (b *bashTool) Run(ctx context.Context, args map[string]any) (string, error) {
	cmd, err := strArg(args, "command")
	if err != nil {
		return "", err
	}
	o, err := b.sb.Run(ctx, cmd)
	if err != nil {
		return o.Combined(), err
	}
	// Compress at the tool-result boundary so the compressed bytes land in the
	// cache prefix (docs/spec.md §5). Never-inflate gate preserves terse output.
	return compress.Compress(o.Combined()), nil
}

// Combined returns stdout then stderr joined, prioritizing stdout for token
// efficiency while keeping stderr visible.
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
