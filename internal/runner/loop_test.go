package runner

import (
	"context"
	"testing"
	"time"

)

func TestRunService_CrashDumpOnFatalError(t *testing.T) {
	// This test verifies that a fatal error (non-cancelled, non-max-turns) in the
	// run goroutine triggers crashDumpFunc via the RunService wiring.
	// We use a minimal LLM config that will fail during RunAgent with a real error.

	var capturedErr string
	var capturedStack []byte
	crashCalled := make(chan struct{}, 1)

	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      nil,
		HistorySessionMgr: nil,
		CrashDumpFunc: func(err error, stack []byte) {
			capturedErr = err.Error()
			capturedStack = stack
			crashCalled <- struct{}{}
		},
	})

	// Start a run with a garbage URL that will fail
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://127.0.0.1:1", // unlikely to have an LLM server
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	_, err := svc.StartRun(context.Background(), "crash-session", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun should not return error directly (goroutine runs async): %v", err)
	}
	defer svc.Cancel("crash-session")

	// Wait for crash dump to be called (or timeout)
	select {
	case <-crashCalled:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for crashDumpFunc to be called")
	}

	if capturedErr == "" {
		t.Error("capturedErr is empty")
	}
	if len(capturedStack) == 0 {
		t.Error("capturedStack is empty")
	}
	// The error should mention connection failure
	t.Logf("Captured error: %s", capturedErr)
}

func TestRunService_CrashDumpNotCalledOnCancel(t *testing.T) {
	// Verify that cancellation does NOT trigger crashDumpFunc.

	crashCalled := false
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      nil,
		HistorySessionMgr: nil,
		CrashDumpFunc: func(err error, stack []byte) {
			crashCalled = true
		},
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	_, err := svc.StartRun(context.Background(), "cancel-session", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Cancel immediately (before RunAgent can connect/error)
	svc.Cancel("cancel-session")

	// Give time for the goroutine to process cancellation
	time.Sleep(500 * time.Millisecond)

	if crashCalled {
		t.Error("crashDumpFunc should NOT be called on cancellation")
	}
}
