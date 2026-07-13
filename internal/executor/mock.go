package executor

import (
	"context"
	"sync"
)

// MockExecutor is a test double for CommandExecutor that returns canned results.
type MockExecutor struct {
	mu       sync.Mutex
	Results  []CommandResult
	Commands []string // history of received commands
	Index    int      // next result to return
	CloseErr error
}

func NewMockExecutor() *MockExecutor {
	return &MockExecutor{}
}

func (m *MockExecutor) ExecuteCommand(ctx context.Context, command string) (CommandResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Commands = append(m.Commands, command)
	if m.Index < len(m.Results) {
		r := m.Results[m.Index]
		m.Index++
		return r, nil
	}
	return CommandResult{Stdout: "", ExitCode: 0}, nil
}

func (m *MockExecutor) Close() error {
	return m.CloseErr
}
