package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/sandbox"
	"github.com/voocel/litellm"
)

const maxBashOutputBytes = 8 * 1024

type bashArgs struct {
	Command string `json:"command" jsonschema:"Shell command to run in the workspace directory"`
}

// BashTool implements ToolHandler for running shell commands.
type BashTool struct {
	workspace     string
	timeout       time.Duration
	sandboxConfig sandbox.Config
	schema        litellm.Schema
}

// NewBashTool creates a new BashTool.
// Pass zero-value sandbox.Config to use the default profile.
func NewBashTool(workspace string, timeout time.Duration, sc sandbox.Config) *BashTool {
	return &BashTool{
		workspace:     workspace,
		timeout:       timeout,
		sandboxConfig: sc,
		schema:        SchemaOf[bashArgs](),
	}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Description() string {
	return "Run a shell command in the workspace. Each call is a fresh shell — chain with && or use env vars to persist state. For commands, tests, builds, or shell operations. Capped at 8 KiB of output."
}

func (t *BashTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *BashTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed bashArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("bash: invalid args: %w", err)
	}

	if parsed.Command == "" {
		return ToolError(TextBlocks("Error: command is required")), nil
	}

	// Apply timeout if configured
	execCtx := ctx
	var cancel context.CancelFunc
	if t.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	// Build command through sandbox wrapper
	execPath, execArgs, cleanup, err := sandbox.WrapCommand(t.workspace, parsed.Command, t.sandboxConfig)
	defer cleanup()
	if err != nil {
		return ToolError(TextBlocks(fmt.Sprintf("Error: sandbox setup failed: %v", err))), nil
	}

	cmd := exec.CommandContext(execCtx, execPath, execArgs...)
	cmd.Dir = t.workspace
	// Ensure TMPDIR points to the ephemeral /tmp inside the sandbox.
	cmd.Env = append(os.Environ(), "TMPDIR=/tmp")

	// Capture stdout and stderr via pipes
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()

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
			return ToolError(TextBlocks(fmt.Sprintf("Error: command execution failed: %v", err))), nil
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

	// Cap raw output at 8 KiB before compression.
	const truncationMarker = "... (output truncated at 8 KiB)"
	if len(output) > maxBashOutputBytes {
		truncLen := maxBashOutputBytes - len(truncationMarker)
		if truncLen < 0 {
			truncLen = 0
		}
		output = output[:truncLen] + truncationMarker
	}

	// Apply pattern compression.
	// compress.Compress returns the original unchanged when no compressor
	// matches or anti-inflation would kick in.
	compressed := compress.Compress(parsed.Command, output)

	// Determine which version to send to the LLM and whether to preserve raw.
	var blocks []litellm.Block
	var rawBlocks []litellm.Block
	if compressed != output {
		blocks = TextBlocks(compressed)
		rawBlocks = TextBlocks(output)
	} else {
		blocks = TextBlocks(output)
		rawBlocks = nil
	}

	if exitCode != 0 || timedOut {
		return ToolResult{Blocks: blocks, RawBlocks: rawBlocks, IsError: true}, nil
	}
	return ToolResult{Blocks: blocks, RawBlocks: rawBlocks}, nil
}
