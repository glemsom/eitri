package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/executor"
)

type terminalExecuteArgs struct {
	Command string `json:"command" jsonschema:"Shell command to run in the session's tmux shell"`
}

// TerminalExecuteTool implements ToolHandler for running shell commands.
type TerminalExecuteTool struct {
	sessionMgr *executor.SessionManager
	schema     litellm.Schema
}

// NewTerminalExecute creates a new TerminalExecuteTool.
func NewTerminalExecute(sessionMgr *executor.SessionManager) *TerminalExecuteTool {
	return &TerminalExecuteTool{
		sessionMgr: sessionMgr,
		schema:     SchemaOf[terminalExecuteArgs](),
	}
}

func (t *TerminalExecuteTool) Name() string {
	return "terminal_execute"
}

func (t *TerminalExecuteTool) Description() string {
	return "Execute a shell command in the session's tmux shell and return the output. Use for running commands, tests, builds, or any shell operations."
}

func (t *TerminalExecuteTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *TerminalExecuteTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed terminalExecuteArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("terminal_execute: invalid args: %w", err), false
	}

	if parsed.Command == "" {
		return textBlocks("Error: command is required"), nil, true
	}

	if t.sessionMgr == nil {
		return textBlocks("Error: session manager not available"), nil, true
	}

	// Extract session ID from context — set by the agent loop
	sessionID, _ := ctx.Value(sessionIDKey).(string)
	if sessionID == "" {
		return textBlocks("Error: session ID not available in context"), nil, true
	}

	exe, err := t.sessionMgr.GetOrCreate(sessionID)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: failed to get session executor: %v", err)), nil, true
	}

	result, err := exe.ExecuteCommand(ctx, parsed.Command)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: command execution failed: %v", err)), nil, true
	}

	// Build output text
	var output string
	if result.Stdout != "" {
		output += result.Stdout
	}
	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += result.Stderr
	}
	if result.ExitCode != 0 {
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("\n[exit code %d]", result.ExitCode)
	}
	if result.TimedOut {
		if output != "" {
			output += "\n"
		}
		output += "\n[command timed out]"
	}
	if result.Truncated {
		if output != "" {
			output += "\n"
		}
		output += "\n[output truncated — 128 KiB limit]"
	}

	return textBlocks(output), nil, result.ExitCode != 0 || result.TimedOut
}
