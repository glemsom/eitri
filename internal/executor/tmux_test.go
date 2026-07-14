package executor_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/executor"
)

func TestTmuxExecutor_RunsCommandAndReturnsOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-session-1", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	result, err := exe.ExecuteCommand(ctx, "echo hello world")
	if err != nil {
		t.Fatalf("ExecuteCommand() = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello world") {
		t.Errorf("Stdout = %q, want contains %q", result.Stdout, "hello world")
	}
	if result.TimedOut {
		t.Errorf("TimedOut = true, want false")
	}
}

func TestTmuxExecutor_ShellStatePersists(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-session-2", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	// Change directory
	_, err = exe.ExecuteCommand(ctx, "cd /tmp")
	if err != nil {
		t.Fatalf("ExecuteCommand(cd) = %v", err)
	}

	// Verify cwd changed
	result, err := exe.ExecuteCommand(ctx, "pwd")
	if err != nil {
		t.Fatalf("ExecuteCommand(pwd) = %v", err)
	}
	if !strings.Contains(result.Stdout, "/tmp") {
		t.Errorf("Stdout = %q, want contains /tmp", result.Stdout)
	}
}

func TestTmuxExecutor_ExitCodeNonZero(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-session-3", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	result, err := exe.ExecuteCommand(ctx, "false")
	if err != nil {
		t.Fatalf("ExecuteCommand(false) = %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}

func TestTmuxExecutor_InitialDirectoryIsWorkspace(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	// Create a unique workspace dir
	dir := t.TempDir()
	// Create a marker file to confirm we're in the right dir
	markerFile := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(markerFile, []byte("here"), 0644); err != nil {
		t.Fatal(err)
	}

	exe, err := executor.NewTmuxExecutor("test-session-4", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	result, err := exe.ExecuteCommand(ctx, "pwd")
	if err != nil {
		t.Fatalf("ExecuteCommand(pwd) = %v", err)
	}
	if !strings.Contains(result.Stdout, dir) {
		t.Errorf("Stdout = %q, want contains %q", result.Stdout, dir)
	}

	// Verify marker file exists in current dir
	result, err = exe.ExecuteCommand(ctx, "ls marker.txt")
	if err != nil {
		t.Fatalf("ExecuteCommand(ls) = %v", err)
	}
	if !strings.Contains(result.Stdout, "marker.txt") {
		t.Errorf("Stdout = %q, want contains marker.txt", result.Stdout)
	}
}

func TestTmuxExecutor_CloseKillsSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-session-5", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}

	// Close the executor
	if err := exe.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// Verify session is dead
	result, err := exe.ExecuteCommand(context.Background(), "echo should fail")
	if err == nil {
		t.Errorf("ExecuteCommand() after Close = %+v, want error", result)
	}
}

func TestTmuxExecutor_ConcurrentRejected(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-concurrent", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	// Start a long command
	type cmdResult struct {
		result executor.CommandResult
		err    error
	}
	ch := make(chan cmdResult, 1)
	go func() {
		r, e := exe.ExecuteCommand(ctx, "sleep 5")
		ch <- cmdResult{r, e}
	}()

	time.Sleep(200 * time.Millisecond) // let the first command start

	// Try concurrent command, should fail
	_, err = exe.ExecuteCommand(ctx, "echo concurrent")
	if err == nil {
		t.Error("concurrent ExecuteCommand should return error")
	} else {
		t.Logf("Concurrent rejection: %v", err)
	}

	// Wait for first command to finish (it'll time out after 10s)
	<-ch
}

func TestTmuxExecutor_CommandTimeout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	// Very short timeout to trigger
	exe, err := executor.NewTmuxExecutor("test-session-6", dir, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx := context.Background()
	// Command that runs longer than the short timeout
	result, err := exe.ExecuteCommand(ctx, "sleep 5")
	if err != nil {
		t.Fatalf("ExecuteCommand(sleep) = %v, want timeout result", err)
	}
	if !result.TimedOut {
		t.Errorf("TimedOut = false, want true for slow command")
	}
}

func TestTmuxExecutor_ContextCancelStopsCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	exe, err := executor.NewTmuxExecutor("test-session-cancel", dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := exe.ExecuteCommand(ctx, "sleep 30")
		resultCh <- err
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	err = <-resultCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteCommand() err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation took too long: %v", time.Since(start))
	}
}

func TestTmuxExecutor_CommandTimeoutKillsChildProcess(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	exe, err := executor.NewTmuxExecutor("test-session-timeout-kill", dir, time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}
	defer exe.Close()

	result, err := exe.ExecuteCommand(context.Background(), "sh -c 'sleep 30 & echo $! > child.pid; wait'")
	if err != nil {
		t.Fatalf("ExecuteCommand() = %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("TimedOut = false, want true")
	}

	pid := waitForPIDFile(t, pidFile)
	waitForProcessExit(t, pid)
}

func TestTmuxExecutor_CloseKillsChildProcess(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found on $PATH, skipping integration test")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	exe, err := executor.NewTmuxExecutor("test-session-close-kill", dir, 30*time.Second)
	if err != nil {
		t.Fatalf("NewTmuxExecutor() = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := exe.ExecuteCommand(context.Background(), "sh -c 'sleep 30 & echo $! > child.pid; wait'")
		errCh <- err
	}()

	pid := waitForPIDFile(t, pidFile)
	if err := exe.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && !strings.Contains(err.Error(), "executor closed") {
			// command may surface context/session interruption or return cleanly after session death
			t.Logf("ExecuteCommand() returned err after close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteCommand() did not return after Close")
	}

	waitForProcessExit(t, pid)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil {
				t.Fatalf("invalid pid file %q: %v", string(data), convErr)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s not written: %v", path, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
