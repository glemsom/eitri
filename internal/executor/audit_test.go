package executor_test

import (
	"testing"

	"github.com/glemsom/eitri/internal/executor"
)

func TestRunAudit_TmuxFound(t *testing.T) {
	err := executor.RunAudit()
	if err != nil {
		t.Fatalf("RunAudit() = %v, want nil (tmux should be on $PATH)", err)
	}
}

func TestRunAudit_TmuxNotFound(t *testing.T) {
	// Temporarily remove tmux from PATH
	originalPath := executor.TmuxPath
	defer func() { executor.TmuxPath = originalPath }()
	executor.TmuxPath = "/nonexistent/tmux"

	err := executor.RunAudit()
	if err == nil {
		t.Fatal("RunAudit() = nil, want error when tmux not found")
	}
}
