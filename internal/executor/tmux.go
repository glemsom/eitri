package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxOutputBytes  = 128 * 1024
	pollInterval    = 50 * time.Millisecond
	interruptGrace  = 100 * time.Millisecond
	termWaitTimeout = 500 * time.Millisecond
	killWaitTimeout = 2 * time.Second
)

var errProcessGroupNotFound = errors.New("process group not found")

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

	startTime := time.Now()
	timeout := e.cmdTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Generate unique sentinel markers per command so stale pane content doesn't match.
	id := fmt.Sprintf("%x", rand.Int63())
	startSentinel := "EITRI_ST_" + id
	endSentinel := "EITRI_EN_" + id
	exitSentinel := "EITRI_EX_" + id

	// Build command with unique sentinel markers.
	wrappedCmd := fmt.Sprintf("echo '%s'; %s; echo \"%s:$?\"; echo '%s'",
		startSentinel, command, exitSentinel, endSentinel)

	// Send command into tmux session, retrying once if session is dead.
	if err := e.sendCommand(wrappedCmd); err != nil {
		return CommandResult{}, fmt.Errorf("send command: %w", err)
	}

	var allOutput string
pollLoop:
	for {
		select {
		case <-commandCtx.Done():
			allOutput = e.capturePane()
			if err := e.stopActiveCommand(); err != nil && !errors.Is(err, errProcessGroupNotFound) {
				return CommandResult{}, err
			}
			if errors.Is(commandCtx.Err(), context.Canceled) {
				return CommandResult{}, commandCtx.Err()
			}
			break pollLoop
		default:
		}

		allOutput = e.capturePane()
		if containsMarkerLine(allOutput, endSentinel) {
			break pollLoop
		}

		// If pane is empty and session appears dead, attempt recovery once.
		if allOutput == "" {
			if e.recoverSession() {
				if err := e.sendCommand(wrappedCmd); err != nil {
					return CommandResult{}, fmt.Errorf("send command after recovery: %w", err)
				}
				continue
			}
			break pollLoop
		}

		time.Sleep(pollInterval)
	}

	duration := time.Since(startTime)
	timedOut := errors.Is(commandCtx.Err(), context.DeadlineExceeded)

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
		Stderr:     "",
		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMs: duration.Milliseconds(),
		Truncated:  truncated,
	}, nil
}

// sendCommand sends a literal string and Enter key to tmux session.
func (e *TmuxExecutor) sendCommand(cmd string) error {
	if out, err := exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "-l", cmd).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %w\n%s", err, string(out))
	}
	if _, err := exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %w", err)
	}
	return nil
}

// recoverSession recreates tmux session if it was killed externally.
// Returns true if recovery succeeded, false if executor is closed.
func (e *TmuxExecutor) recoverSession() bool {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return false
	}
	e.mu.Unlock()

	_ = exec.Command(TmuxPath, "kill-session", "-t", e.sessionName).Run()
	cmd := exec.Command(TmuxPath, "new-session", "-d", "-s", e.sessionName, "-c", e.workspace)

	slog.Warn("tmux session missing; recreating", slog.String("tmux_session", e.sessionName))
	if _, err := cmd.CombinedOutput(); err != nil {
		return false
	}
	time.Sleep(200 * time.Millisecond)
	return true
}

// capturePane returns current pane content for session.
func (e *TmuxExecutor) capturePane() string {
	cmd := exec.Command(TmuxPath, "capture-pane", "-t", e.sessionName, "-p", "-J")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

// Close kills active process group and tmux session.
func (e *TmuxExecutor) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	slog.Info("tmux executor closing", slog.String("tmux_session", e.sessionName))
	e.mu.Unlock()

	stopErr := e.stopActiveCommand()
	killErr := e.killSession()
	if killErr != nil {
		return killErr
	}
	if stopErr != nil && !errors.Is(stopErr, errProcessGroupNotFound) {
		return stopErr
	}
	return nil
}

func (e *TmuxExecutor) stopActiveCommand() error {
	pid, err := e.panePID()
	if err != nil {
		if isMissingTmuxSession(err.Error()) {
			return errProcessGroupNotFound
		}
		return err
	}

	targets := collectProcessTargets(pid)
	_ = exec.Command(TmuxPath, "send-keys", "-t", e.sessionName, "C-c").Run()
	time.Sleep(interruptGrace)
	return killProcessTargets(targets)
}

func (e *TmuxExecutor) panePID() (int, error) {
	out, err := exec.Command(TmuxPath, "display-message", "-p", "-t", e.sessionName, "#{pane_pid}").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("tmux display-message: %w\n%s", err, string(out))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse pane pid %q: %w", strings.TrimSpace(string(out)), err)
	}
	return pid, nil
}

func (e *TmuxExecutor) killSession() error {
	out, err := exec.Command(TmuxPath, "kill-session", "-t", e.sessionName).CombinedOutput()
	if err != nil {
		if isMissingTmuxSession(string(out)) {
			return nil
		}
		return fmt.Errorf("tmux kill-session: %w\n%s", err, string(out))
	}
	return nil
}

type processTargets struct {
	pids  map[int]struct{}
	pgids map[int]struct{}
}

func collectProcessTargets(rootPID int) processTargets {
	targets := processTargets{
		pids:  make(map[int]struct{}),
		pgids: make(map[int]struct{}),
	}
	queue := []int{rootPID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 0 {
			continue
		}
		if _, seen := targets.pids[pid]; seen {
			continue
		}
		if !processExists(pid) {
			continue
		}
		targets.pids[pid] = struct{}{}
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
			targets.pgids[pgid] = struct{}{}
		}
		queue = append(queue, childPIDs(pid)...)
	}
	return targets
}

func childPIDs(pid int) []int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(data))
	children := make([]int, 0, len(fields))
	for _, field := range fields {
		childPID, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		children = append(children, childPID)
	}
	return children
}

func killProcessTargets(targets processTargets) error {
	if len(targets.pids) == 0 {
		return errProcessGroupNotFound
	}
	if err := signalProcessTargets(targets, syscall.SIGTERM); err != nil {
		return err
	}
	if waitForProcessTargetsExit(targets, termWaitTimeout) {
		return nil
	}
	if err := signalProcessTargets(targets, syscall.SIGKILL); err != nil {
		return err
	}
	if waitForProcessTargetsExit(targets, killWaitTimeout) {
		return nil
	}
	return fmt.Errorf("process tree did not exit")
}

func signalProcessTargets(targets processTargets, sig syscall.Signal) error {
	for pgid := range targets.pgids {
		if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("send %s to process group %d: %w", sig.String(), pgid, err)
		}
	}
	for pid := range targets.pids {
		if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("send %s to pid %d: %w", sig.String(), pid, err)
		}
	}
	return nil
}

func waitForProcessTargetsExit(targets processTargets, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		allGone := true
		for pid := range targets.pids {
			if processExists(pid) {
				allGone = false
				break
			}
		}
		if allGone {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isMissingTmuxSession(msg string) bool {
	return strings.Contains(msg, "can't find session")
}

// containsMarkerLine reports whether marker appears as its own output line.
func containsMarkerLine(s, marker string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// extractBetween returns content between marker lines, ignoring prompt/input lines
// where sentinels appear as part of typed shell command.
func extractBetween(s, startMarker, endMarker string) string {
	lines := strings.Split(s, "\n")
	startIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == startMarker {
			startIdx = i
		}
	}
	if startIdx < 0 {
		return s
	}

	endIdx := len(lines)
	for i := startIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == endMarker {
			endIdx = i
			break
		}
	}
	return strings.Join(lines[startIdx+1:endIdx], "\n")
}

// parseExitCode extracts exit code from output using given exit marker.
func parseExitCode(s, exitMarker string) int {
	prefix := exitMarker + ":"
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			code := 0
			fmt.Sscanf(strings.TrimPrefix(line, prefix), "%d", &code)
			return code
		}
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
