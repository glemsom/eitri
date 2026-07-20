package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/voocel/litellm"
)

const maxBashOutputBytes = 128 * 1024

type bashArgs struct {
	Command string `json:"command" jsonschema:"Shell command to run in the workspace directory"`
}

// BashTool implements ToolHandler for running shell commands.
type BashTool struct {
	workspace string
	timeout   time.Duration
	schema    litellm.Schema
}

// NewBashTool creates a new BashTool.
func NewBashTool(workspace string, timeout time.Duration) *BashTool {
	return &BashTool{
		workspace: workspace,
		timeout:   timeout,
		schema:    SchemaOf[bashArgs](),
	}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "Run a shell command in the workspace. Each call is a fresh shell — chain with && or use env vars to persist state. For commands, tests, builds, or shell operations."
}

func (t *BashTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *BashTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed bashArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("bash: invalid args: %w", err), false
	}

	if parsed.Command == "" {
		return textBlocks("Error: command is required"), nil, true
	}

	// Apply timeout if configured
	execCtx := ctx
	var cancel context.CancelFunc
	if t.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", parsed.Command)
	cmd.Dir = t.workspace

	// Capture stdout and stderr via pipes
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

	var exitCode int
	var timedOut bool

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			// Check if this was a timeout: the context deadline was exceeded
			if execCtx.Err() != nil {
				timedOut = true
			}
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			timedOut = true
		} else {
			return textBlocks(fmt.Sprintf("Error: command execution failed: %v", err)), nil, true
		}
	}

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()

	// Trim trailing newlines so output between tags is clean
	stdoutStr = strings.TrimRight(stdoutStr, "\n")
	stderrStr = strings.TrimRight(stderrStr, "\n")

	// Build output text with spec-compliant format
	var output string
	if stdoutStr != "" {
		output += "<stdout>\n" + stdoutStr + "\n</stdout>"
	}
	if stderrStr != "" {
		if output != "" {
			output += "\n"
		}
		output += "<stderr>\n" + stderrStr + "\n</stderr>"
	}
	if exitCode != 0 {
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("[exit code %d]", exitCode)
	}
	if timedOut {
		if output != "" {
			output += "\n"
		}
		output += "[command timed out]"
	}

	// Truncate if output exceeds limit
	const truncationMarker = "... (output truncated at 128 KiB)"
	if len(output) > maxBashOutputBytes {
		truncLen := maxBashOutputBytes - len(truncationMarker)
		if truncLen < 0 {
			truncLen = 0
		}
		output = output[:truncLen] + truncationMarker
	}

	return textBlocks(output), nil, exitCode != 0 || timedOut
}
