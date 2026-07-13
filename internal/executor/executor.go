// Package executor provides command execution via tmux.
//
// The central interface is CommandExecutor, with a real tmux implementation
// (TmuxExecutor) and a MockExecutor for tests.
package executor

import (
	"context"
)

// CommandResult holds the outcome of a single command execution.
type CommandResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out"`
	DurationMs int64 `json:"duration_ms"`
	Truncated bool  `json:"truncated"`
}

// CommandExecutor abstracts command execution inside a long-running shell.
//
// ExecuteCommand runs one command and returns its result. Only one command
// may execute at a time per executor; concurrent calls return an error.
// The shell state (cwd, env) persists between commands.
//
// Close kills the underlying process group and releases resources.
type CommandExecutor interface {
	ExecuteCommand(ctx context.Context, command string) (CommandResult, error)
	Close() error
}
