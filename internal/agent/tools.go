// Package agent defines built-in tools for the ADK agent.
package agent

import (
	"context"

	"github.com/glemsom/eitri/internal/executor"
)

// ToolResult is the result returned by a built-in tool.
type ToolResult struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// TerminalExecuteArgs is the argument schema for terminal_execute.
type TerminalExecuteArgs struct {
	// jsonschema tag value is the field description; required is inferred from json tag (no omitempty).
	Command string `json:"command" jsonschema:"Shell command to execute in the session's tmux shell"`
}

// NewTerminalExecuteHandler creates a handler for the terminal_execute tool.
func NewTerminalExecuteHandler(sessionMgr *executor.SessionManager) func(ctx context.Context, sessionID string, args TerminalExecuteArgs) ToolResult {
	return func(ctx context.Context, sessionID string, args TerminalExecuteArgs) ToolResult {
		exe, err := sessionMgr.GetOrCreate(sessionID)
		if err != nil {
			return ToolResult{
				Success: false,
				Error:   "Failed to get session executor: " + err.Error(),
			}
		}

		result, err := exe.ExecuteCommand(ctx, args.Command)
		if err != nil {
			return ToolResult{
				Success: false,
				Error:   "Command execution failed: " + err.Error(),
			}
		}

		return ToolResult{
			Success: true,
			Data:    result,
		}
	}
}

