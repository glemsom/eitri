package executor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glemsom/eitri/internal/executor"
)

func TestMockExecutor_ReturnsCannedResult(t *testing.T) {
	m := executor.NewMockExecutor()
	m.Results = []executor.CommandResult{
		{Stdout: "hello", ExitCode: 0},
		{Stdout: "world", ExitCode: 1},
	}

	r1, err := m.ExecuteCommand(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("ExecuteCommand[0] err = %v", err)
	}
	if r1.Stdout != "hello" || r1.ExitCode != 0 {
		t.Errorf("got %+v, want {Stdout:hello ExitCode:0}", r1)
	}

	r2, err := m.ExecuteCommand(context.Background(), "echo world")
	if err != nil {
		t.Fatalf("ExecuteCommand[1] err = %v", err)
	}
	if r2.Stdout != "world" || r2.ExitCode != 1 {
		t.Errorf("got %+v, want {Stdout:world ExitCode:1}", r2)
	}

	// Exhausted results returns empty
	r3, _ := m.ExecuteCommand(context.Background(), "extra")
	if r3.Stdout != "" || r3.ExitCode != 0 {
		t.Errorf("exhausted result = %+v, want empty", r3)
	}

	if len(m.Commands) != 3 {
		t.Errorf("Commands recorded = %d, want 3", len(m.Commands))
	}
}

func TestMockExecutor_Close(t *testing.T) {
	m := executor.NewMockExecutor()
	if err := m.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	m.CloseErr = nil // default is nil
}

func TestCommandResult_JSONShape(t *testing.T) {
	result := executor.CommandResult{
		Stdout:     "out",
		Stderr:     "",
		ExitCode:   0,
		TimedOut:   false,
		DurationMs: 1234,
		Truncated:  false,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}
	want := `{"stdout":"out","stderr":"","exit_code":0,"timed_out":false,"duration_ms":1234,"truncated":false}`
	if string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
}
