package executor

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxOutputBytes = 128 * 1024
	pollInterval   = 50 * time.Millisecond
)

// TmuxExecutor implements CommandExecutor via a persistent tmux session.
type TmuxExecutor struct {
	sessionName string
	workspace   string
	cmdTimeout  time.Duration

	mu      sync.Mutex
	closed  bool
	running bool
}

// NewTmuxExecutor creates a new tmux session in the given workspace directory.
func NewTmuxExecutor(sessionName, workspace string, cmdTimeout time.Duration) (*TmuxExecutor, error) {
	_ = exec.Command(TmuxPath, "kill-session", "-t", sessionName).Run()

	cmd := exec.Command(TmuxPath, "new-session", "-d", "-s", sessionName, "-c", workspace)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %w\n%s", err, string(out))
	}

	time.Sleep(200 * time.Millisecond)

	return &TmuxExecutor{
		sessionName: sessionName,
		workspace:   workspace,
		cmdTimeout:  cmdTimeout,
	}, nil
}

// ExecuteCommand runs a shell command inside the tmux session and returns the result.
// Uses unique per-command sentinel markers to reliably detect command boundaries.
func (e *TmuxExecutor) ExecuteCommand(ctx context.Context, command string) (CommandResult, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return CommandResult{}, fmt.Errorf("executor closed")
	}
	e.mu.Unlock()

	startTime := time.Now()

	// Reject concurrent commands per spec §4.2
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return CommandResult{}, fmt.Errorf("a command is already running in this session")
	}
	e.running = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	timeout := e.cmdTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Generate unique sentinel markers per command so stale pane content doesn't match.
	id := fmt.Sprintf("%x", rand.Int63())
	startSentinel := "EITRI_ST_" + id
	endSentinel := "EITRI_EN_" + id
	exitSentinel := "EITRI_EX_" + id

	// Build command with unique sentinel markers
	// echo startSentinel; <command>; echo "exitSentinel:$?"; echo endSentinel
	wrappedCmd := fmt.Sprintf("echo '%s'; %s; echo \"%s:$?\"; echo '%s'",
		startSentinel, command, exitSentinel, endSentinel)

	// Send command into the tmux session, retrying once if session is dead.
	if err := e.sendCommand(wrappedCmd); err != nil {
		return CommandResult{}, fmt.Errorf("send command: %w", err)
	}

	// Poll for command completion.
	// The sentinel appears in BOTH the echoed command line and the actual output.
	// Waiting for 2+ occurrences means the command has finished executing.
	var allOutput string
pollLoop:
	for {
		select {
		case <-timeoutCtx.Done():
			exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "C-c").Run()
			time.Sleep(100 * time.Millisecond)
			allOutput = e.capturePane()
			break pollLoop
		default:
		}

		allOutput = e.capturePane()
		if strings.Count(allOutput, endSentinel) >= 2 {
			break pollLoop
		}

		// If pane is empty and session appears dead, attempt recovery once.
		if allOutput == "" {
			if e.recoverSession() {
				// Session restored — re-send the command.
				if err := e.sendCommand(wrappedCmd); err != nil {
					return CommandResult{}, fmt.Errorf("send command after recovery: %w", err)
				}
				continue
			}
			// Recovery failed — exit poll loop.
			break pollLoop
		}

		time.Sleep(pollInterval)
	}

	duration := time.Since(startTime)
	timedOut := timeoutCtx.Err() != nil

	// Extract output between start sentinel and end sentinel
	output := extractBetween(allOutput, startSentinel, endSentinel)
	exitCode := parseExitCode(output, exitSentinel)
	output = stripLineContaining(output, exitSentinel)
	output = strings.TrimSpace(output)

	truncated := false
	if len(output) > maxOutputBytes {
		output = output[:maxOutputBytes]
		truncated = true
	}

	return CommandResult{
		Stdout:     output,
		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMs: duration.Milliseconds(),
		Truncated:  truncated,
	}, nil
}

// sendCommand sends a literal string and Enter key to the tmux session.
func (e *TmuxExecutor) sendCommand(cmd string) error {
	if out, err := exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "-l", cmd).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w\n%s", err, string(out))
	}
	if _, err := exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w", err)
	}
	return nil
}

// recoverSession recreates the tmux session if it was killed externally.
// Returns true if recovery succeeded, false if the executor is closed.
func (e *TmuxExecutor) recoverSession() bool {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return false
	}
	e.mu.Unlock()

	_ = exec.Command(TmuxPath, "kill-session", "-t", e.sessionName).Run()
	cmd := exec.Command(TmuxPath, "new-session", "-d", "-s", e.sessionName, "-c", e.workspace)
	if _, err := cmd.CombinedOutput(); err != nil {
		return false
	}
	time.Sleep(200 * time.Millisecond)
	return true
}

// capturePane returns the current pane content for the session.
func (e *TmuxExecutor) capturePane() string {
	cmd := exec.Command(TmuxPath, "capture-pane", "-t", e.sessionName, "-p", "-J")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

// Close kills the tmux session.
func (e *TmuxExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return exec.Command(TmuxPath, "kill-session", "-t", e.sessionName).Run()
}

// extractBetween returns content after the LAST occurrence of startMarker
// and before the FIRST occurrence of endMarker after that start.
func extractBetween(s, startMarker, endMarker string) string {
	startIdx := strings.LastIndex(s, startMarker)
	if startIdx < 0 {
		return s
	}
	startIdx += len(startMarker)

	after := s[startIdx:]
	endIdx := strings.Index(after, endMarker)
	if endIdx < 0 {
		return after
	}
	return after[:endIdx]
}

// parseExitCode extracts exit code from output using the given exit marker.
func parseExitCode(s, exitMarker string) int {
	re := regexp.MustCompile(exitMarker + `:(\d+)`)
	m := re.FindStringSubmatch(s)
	if len(m) >= 2 {
		code := 0
		fmt.Sscanf(m[1], "%d", &code)
		return code
	}
	return 0
}

// stripLineContaining removes any line that contains substr.
func stripLineContaining(s, substr string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, substr) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
