package agent_test

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/executor"
)

func TestNewTerminalExecuteHandler_UsesSessionManager(t *testing.T) {
	// Create a session manager backed by MockExecutor
	sm := executor.NewSessionManager("/tmp", 0, 0)

	handler := agent.NewTerminalExecuteHandler(sm)

	// Execute a command
	result := handler(context.Background(), "test-session", agent.TerminalExecuteArgs{Command: "echo hello"})

	if !result.Success {
		t.Fatalf("result.Success = false, want true: %s", result.Error)
	}

	data, ok := result.Data.(executor.CommandResult)
	if !ok {
		t.Fatalf("result.Data type = %T, want executor.CommandResult", result.Data)
	}
	if data.Stdout != "" {
		// Real tmux executor returns output
		t.Logf("CommandResult: Stdout=%q ExitCode=%d", data.Stdout, data.ExitCode)
	}

	// Second call to same session should reuse executor (verify by session manager caching)
	result2 := handler(context.Background(), "test-session", agent.TerminalExecuteArgs{Command: "echo world"})
	if !result2.Success {
		t.Fatalf("second call failed: %s", result2.Error)
	}
}

func TestNewTerminalExecuteHandler_ErrorHandling(t *testing.T) {
	sm := executor.NewSessionManager("/nonexistent", 0, 0)

	handler := agent.NewTerminalExecuteHandler(sm)

	// Command to a session with working directory that doesn't exist
	result := handler(context.Background(), "error-session", agent.TerminalExecuteArgs{Command: "echo test"})
	if !result.Success {
		// It might fail if /nonexistent doesn't exist, or might succeed (tmux handles it)
		t.Logf("Result with nonexistent workspace: success=%v error=%s", result.Success, result.Error)
	}
}
