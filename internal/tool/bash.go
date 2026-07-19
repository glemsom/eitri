package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/voocel/litellm"
)

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
	return "Execute a shell command in the workspace directory and return the output. Each call runs in a fresh shell — chain commands with && or use env vars to persist state between calls. Use for running commands, tests, builds, or any shell operations."
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

	// Build output text
	var output string
	if stdoutStr != "" {
		output += stdoutStr
	}
	if stderrStr != "" {
		if output != "" {
			output += "\n"
		}
		output += stderrStr
	}
	if exitCode != 0 {
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("\n[exit code %d]", exitCode)
	}
	if timedOut {
		if output != "" {
			output += "\n"
		}
		output += "\n[command timed out]"
	}

	return textBlocks(output), nil, exitCode != 0 || timedOut
}
