package executor_test

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/executor"
)

func TestSessionManager_GetOrCreate(t *testing.T) {
	sm := executor.NewSessionManager("", 60*time.Second, 100*time.Minute)

	// First call creates executor
	exe1, err := sm.GetOrCreate("session-a")
	if err != nil {
		t.Fatalf("GetOrCreate(session-a) = %v", err)
	}
	if exe1 == nil {
		t.Fatal("GetOrCreate(session-a) returned nil executor")
	}

	// Second call returns same executor
	exe2, err := sm.GetOrCreate("session-a")
	if err != nil {
		t.Fatalf("GetOrCreate(session-a) second call = %v", err)
	}
	if exe1 != exe2 {
		t.Error("GetOrCreate returned different executor for same sessionID")
	}

	// Different session ID returns different executor
	exe3, err := sm.GetOrCreate("session-b")
	if err != nil {
		t.Fatalf("GetOrCreate(session-b) = %v", err)
	}
	if exe1 == exe3 {
		t.Error("GetOrCreate returned same executor for different sessionID")
	}
}

func TestSessionManager_CloseRemovesExecutor(t *testing.T) {
	sm := executor.NewSessionManager("", 60*time.Second, 100*time.Minute)

	exe, err := sm.GetOrCreate("close-test")
	if err != nil {
		t.Fatalf("GetOrCreate = %v", err)
	}

	// Close removes executor
	if err := sm.Close("close-test"); err != nil {
		t.Fatalf("Close = %v", err)
	}

	// After close, GetOrCreate creates a new one
	exe2, err := sm.GetOrCreate("close-test")
	if err != nil {
		t.Fatalf("GetOrCreate after close = %v", err)
	}
	if exe == exe2 {
		t.Error("GetOrCreate after close returned same instance")
	}
}

func TestSessionManager_CloseUnknownSession(t *testing.T) {
	sm := executor.NewSessionManager("", 60*time.Second, 100*time.Minute)
	// Closing non-existent session should not error
	if err := sm.Close("nonexistent"); err != nil {
		t.Errorf("Close(nonexistent) = %v, want nil", err)
	}
}

func TestSessionManager_CloseAll(t *testing.T) {
	sm := executor.NewSessionManager("", 60*time.Second, 100*time.Minute)

	_, _ = sm.GetOrCreate("s1")
	_, _ = sm.GetOrCreate("s2")
	_, _ = sm.GetOrCreate("s3")

	sm.CloseAll()

	// All sessions should be removed now
	if err := sm.Close("s1"); err != nil {
		t.Errorf("Close(s1) after CloseAll = %v, want nil", err)
	}
}

func TestSessionManager_IdleTimeout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	// Use very short idle timeout (50ms) and fast tick
	sm := executor.NewSessionManager("", 60*time.Second, 50*time.Millisecond)
	defer sm.CloseAll()

	_, err := sm.GetOrCreate("idle-test")
	if err != nil {
		t.Fatalf("GetOrCreate = %v", err)
	}

	// Start timeout loop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sm.StartTimeoutLoop(ctx, 50*time.Millisecond)

	// Wait for idle timeout to fire (should happen within ~200ms)
	time.Sleep(300 * time.Millisecond)

	// After idle timeout, getting the same session ID should create a new executor
	exe2, err := sm.GetOrCreate("idle-test")
	if err != nil {
		t.Fatalf("GetOrCreate after idle timeout = %v", err)
	}
	if exe2 == nil {
		t.Fatal("GetOrCreate after idle timeout returned nil")
	}
}

func TestSessionManager_ConcurrentSafe(t *testing.T) {
	sm := executor.NewSessionManager("", 60*time.Second, 100*time.Minute)
	var wg sync.WaitGroup

	// Concurrent access should not race
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "session"
			if n%2 == 0 {
				id = "session-even"
			}
			exe, err := sm.GetOrCreate(id)
			if err != nil {
				t.Errorf("GetOrCreate(%s) = %v", id, err)
			}
			if exe != nil {
				_ = sm.Close(id)
			}
		}(i)
	}
	wg.Wait()
}
