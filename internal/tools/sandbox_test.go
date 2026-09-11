package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/compress"
)

type recordingRunner struct {
	calls []RunSpec
	out   *Output
	err   error
}

func (r *recordingRunner) Run(_ context.Context, spec RunSpec) (*Output, error) {
	r.calls = append(r.calls, spec)
	return r.out, r.err
}

func TestSandboxBuildsBwrapArgv(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	tempHost := filepath.Join(t.TempDir(), "tmp")
	sb, newErr := NewSandbox("/home/u/proj", tempHost, rr, "/tmp/kubeconfig")
	if newErr != nil {
		t.Fatalf("NewSandbox() error = %v", newErr)
	}
	_, err := sb.Run(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	spec := rr.calls[0]
	if spec.Name != "bwrap" {
		t.Fatalf("exec name = %q, want bwrap", spec.Name)
	}
	argv := spec.Args
	if argv[0] != "--die-with-parent" {
		t.Fatalf("argv[0] = %q, want --die-with-parent", argv[0])
	}
	want := []string{
		"--die-with-parent",
		"--share-net", // host network
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--ro-bind", filepath.Join(tempHost, sshConfigDirName), "/etc/ssh/ssh_config.d",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/dev/shm",
		"--bind", "/home/u/proj", "/home/u/proj",
		"--bind", tempHost, tempHost,
		"--bind", "/tmp/kubeconfig", "/tmp/kubeconfig",
		"--setenv", "TMPDIR", tempHost,
		"--setenv", "TEMP", tempHost,
		"--setenv", "TMP", tempHost,
		"--chdir", "/home/u/proj",
		"/bin/bash", "-c",
	}
	if len(argv) < len(want)+1 {
		t.Fatalf("argv too short: %v", argv)
	}
	for i, w := range want {
		if argv[i] != w {
			t.Fatalf("argv[%d] = %q, want %q (argv=%v)", i, argv[i], w, argv)
		}
	}
	last := argv[len(argv)-1]
	if last != "echo hi" {
		t.Fatalf("last argv = %q, want command %q", last, "echo hi")
	}
}

func TestSandboxRunPropagatesOutput(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "hello\n", Stderr: "warn\n"}}
	sb, newErr := NewSandbox("/ws", t.TempDir(), rr)
	if newErr != nil {
		t.Fatalf("NewSandbox() error = %v", newErr)
	}
	o, err := sb.Run(context.Background(), "ls")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if o.Stdout != "hello\n" || o.Stderr != "warn\n" {
		t.Fatalf("output = %+v, want stdout=hello stderr=warn", o)
	}
}

func TestSandboxRunPropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	rr := &recordingRunner{err: sentinel}
	sb, newErr := NewSandbox("/ws", t.TempDir(), rr)
	if newErr != nil {
		t.Fatalf("NewSandbox() error = %v", newErr)
	}
	_, err := sb.Run(context.Background(), "false")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want sentinel", err)
	}
}

func TestSandboxRegistersSshConfigMount(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	tempHost := t.TempDir()
	sb, newErr := NewSandbox("/ws", tempHost, rr)
	if newErr != nil {
		t.Fatalf("NewSandbox() error = %v", newErr)
	}
	if _, err := sb.Run(context.Background(), "true"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	argv := rr.calls[0].Args
	sshSrc := tempHost + string(filepath.Separator) + sshConfigDirName
	if !hasArgvPair(argv, "--ro-bind", sshSrc, "/etc/ssh/ssh_config.d") {
		t.Fatalf("argv does not bind sanitized ssh config over /etc/ssh/ssh_config.d: %v", argv)
	}
	if !hasArgvPair(argv, "--ro-bind", "/", "/") {
		t.Fatalf("root is not re-mounted read-only: %v", argv)
	}
}

func hasArgvPair(argv []string, opt, src, dst string) bool {
	for i, a := range argv {
		if a == opt && i+2 < len(argv) && argv[i+1] == src && argv[i+2] == dst {
			return true
		}
	}
	return false
}

func TestSandboxRealBwrapIntegration(t *testing.T) {
	t.Parallel()
	if !bwrapAvailable() {
		t.Skip("bwrap not present; skipping real sandbox test")
	}
	ws := newNonRemappedWorkspace(t)
	tempHost := t.TempDir()
	sb, newErr := NewSandbox(ws, tempHost, defaultRunner{})
	if newErr != nil {
		t.Fatalf("NewSandbox() error = %v", newErr)
	}
	o, err := sb.Run(context.Background(), "cwd=$PWD; touch workspace-gone.txt; echo \"$cwd|workspace-written\" > $PWD/probe.txt; echo done")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if strings.TrimSpace(o.Stdout) != "done" {
		t.Fatalf("stdout = %q, want done", o.Stdout)
	}
	if _, err := os.Stat(ws + "/probe.txt"); err != nil {
		t.Fatalf("workspace write did not land host-side: %v", err)
	}
	if _, err := sb.Run(context.Background(), "test \"$TMPDIR\" = "+shellQuote(tempHost)+" && test \"$TEMP\" = \"$TMPDIR\" && test \"$TMP\" = \"$TMPDIR\""); err != nil {
		t.Fatalf("session temp env not set: %v", err)
	}
	if _, err := sb.Run(context.Background(), "echo tmp-data > \"$TMPDIR/inside.tmp\""); err != nil {
		t.Fatalf("session temp write error = %v", err)
	}
	if _, err := os.Stat(tempHost + "/inside.tmp"); err != nil {
		t.Fatalf("sandbox $TMPDIR write did not land in session temp host dir: %v", err)
	}
	hostTmp, err := os.CreateTemp("", "eitri-host-tmp-*.txt")
	if err != nil {
		t.Fatalf("create host tmp probe: %v", err)
	}
	hostTmpPath := hostTmp.Name()
	if _, err := hostTmp.WriteString("host-tmp-visible\n"); err != nil {
		t.Fatalf("write host tmp probe: %v", err)
	}
	if err := hostTmp.Close(); err != nil {
		t.Fatalf("close host tmp probe: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(hostTmpPath) })
	if o, err := sb.Run(context.Background(), "cat "+shellQuote(hostTmpPath)); err != nil || o == nil || strings.TrimSpace(o.Stdout) != "host-tmp-visible" {
		out := ""
		if o != nil {
			out = o.Combined()
		}
		t.Fatalf("host /tmp file not readable in sandbox: out=%q err=%v", out, err)
	}
	if _, err := sb.Run(context.Background(), "echo denied > /tmp/eitri-should-not-write"); err == nil {
		t.Fatal("sandbox wrote host /tmp without extra writable path; want read-only failure")
	}
	extraTmp, err := os.CreateTemp("", "eitri-extra-writable-*.txt")
	if err != nil {
		t.Fatalf("create extra writable probe: %v", err)
	}
	extraTmpPath := extraTmp.Name()
	if err := extraTmp.Close(); err != nil {
		t.Fatalf("close extra writable probe: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(extraTmpPath) })
	sbWithExtra, _ := NewSandbox(ws, tempHost, defaultRunner{}, extraTmpPath)
	if _, err := sbWithExtra.Run(context.Background(), "echo allowed > "+shellQuote(extraTmpPath)); err != nil {
		t.Fatalf("extra writable /tmp file was not writable: %v", err)
	}
	data, err := os.ReadFile(extraTmpPath)
	if err != nil || strings.TrimSpace(string(data)) != "allowed" {
		t.Fatalf("extra writable file content = %q err=%v, want allowed", string(data), err)
	}
	if _, err := sb.Run(context.Background(), "test \"$(cat /proc/1/comm)\" = bwrap || exit 1"); err != nil {
		t.Fatalf("sandbox /proc is not pid-namespace-scoped: %v", err)
	}
	if _, err := sb.Run(context.Background(), "test -c /dev/null && test -c /dev/zero || exit 1"); err != nil {
		t.Fatalf("sandbox /dev lacks devtmpfs device nodes: %v", err)
	}
	if _, err := sb.Run(context.Background(), "touch /dev/shm/shm-probe && test -f /dev/shm/shm-probe || exit 1"); err != nil {
		t.Fatalf("sandbox /dev/shm not writable: %v", err)
	}
	sshBin, _ := exec.LookPath("ssh")
	if sshBin != "" {
		if _, err := sb.Run(context.Background(), "ssh -G github.com >/dev/null"); err != nil {
			t.Fatalf("ssh -G inside sandbox failed: %v", err)
		}
	} else {
		t.Log("ssh not present; skipping ssh -G regression check")
	}

	gitBin, _ := exec.LookPath("git")
	if gitBin != "" && sshBin != "" {
		o, err := sb.Run(context.Background(), "git ls-remote git@github.com:glemsom/eitri.git >/dev/null")
		switch {
		case err == nil:
		case strings.Contains(o.Stderr, "Bad owner or permissions"):
			t.Fatalf("git ls-remote hit the ownership error inside the cage: %v\n%s", err, o.Stderr)
		default:
			t.Logf("git ls-remote not verifiable (no network/creds): %v", err)
		}
	} else {
		t.Log("git or ssh not present; skipping git ls-remote regression check")
	}
}

func newNonRemappedWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), ".eitri-test-ws-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return ws
}

func bwrapAvailable() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var _ = os.Getenv

func TestNewSandboxRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workspace string
		tempHost  string
		runner    Runner
		want      string
	}{
		{name: "empty workspace", tempHost: "/tmp/session", runner: &recordingRunner{}, want: "workspace path is empty"},
		{name: "relative workspace", workspace: "workspace", tempHost: "/tmp/session", runner: &recordingRunner{}, want: "workspace path must be absolute"},
		{name: "empty session temp", workspace: "/workspace", runner: &recordingRunner{}, want: "session temp path is empty"},
		{name: "relative session temp", workspace: "/workspace", tempHost: "session", runner: &recordingRunner{}, want: "session temp path must be absolute"},
		{name: "missing runner", workspace: "/workspace", tempHost: "/tmp/session", want: "command runner is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSandbox(tt.workspace, tt.tempHost, tt.runner)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewSandbox() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestDefaultRunnerReapsDescendantsOnCancel(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	pidFile := filepath.Join(temp, "child.pid")

	// Start a background sleep and foreground wait so the shell stays alive
	// until we cancel. The background sleep inherits stdout/stderr.
	cmd := fmt.Sprintf("sleep 3600 & echo $! > %s; wait", pidFile)

	ctx, cancel := context.WithCancel(context.Background())

	// Wait for the pid file (meaning the shell has forked the background
	// child and written its PID), then cancel the context.
	go func() {
		for {
			if _, err := os.Stat(pidFile); err == nil {
				time.Sleep(50 * time.Millisecond)
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	done := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = (defaultRunner{}).Run(ctx, RunSpec{Name: "/bin/bash", Args: []string{"-c", cmd}})
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return promptly after cancellation")
	}

	if runErr == nil {
		t.Fatal("Run() error = nil, want non-nil after cancellation")
	}

	pidBytes, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("failed to read pid file: %v", readErr)
	}
	pidStr := strings.TrimSpace(string(pidBytes))
	if pidStr == "" {
		t.Fatal("pid file is empty")
	}
	pid, convErr := strconv.Atoi(pidStr)
	if convErr != nil {
		t.Fatalf("invalid pid %q: %v", pidStr, convErr)
	}

	awaitTerminated(t, pid)
}

// awaitTerminated waits for the descendant to stop running. SIGKILL delivery
// and the reaping of orphaned grandchildren happen after Run returns, so the
// descendant may briefly remain observable as a live or zombie entry.
func awaitTerminated(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		switch procState(pid) {
		case "", "Z":
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child process %d survived cancellation (state %q)", pid, procState(pid))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// procState returns the /proc status letter for pid, or "" once it is gone.
func procState(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	// comm is parenthesised and may contain spaces, so state follows the last ')'
	fields := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+1:])
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func TestDefaultRunnerBoundsLongRunningCommandOutput(t *testing.T) {
	t.Parallel()
	const emitted = 8 << 20
	o, err := (defaultRunner{}).Run(context.Background(), RunSpec{Name: "/bin/bash", Args: []string{"-c", "head -c 8388608 /dev/zero; head -c 8388608 /dev/zero >&2"}})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	for name, got := range map[string]string{"stdout": o.Stdout, "stderr": o.Stderr} {
		if len(got) != compress.DefaultByteCap {
			t.Errorf("%s retained %d bytes, want exactly %d (the memory bound keeps the full head)", name, len(got), compress.DefaultByteCap)
		}
		if strings.Contains(got, "bytes truncated") {
			t.Errorf("%s carries a text truncation marker; the sandbox buffer must not report (the compress byte cap is the single authority), tail = %q", name, got[max(0, len(got)-40):])
		}
	}
	// Each stream emitted 8 MiB; the buffer rejected everything past the 64 KiB head.
	if want := 2 * (emitted - compress.DefaultByteCap); o.Dropped != want {
		t.Errorf("Output.Dropped = %d, want %d (both streams' rejected bytes)", o.Dropped, want)
	}
}

func TestBoundedBufferAtLimitRetainsVerbatim(t *testing.T) {
	t.Parallel()
	buf := newBoundedBuffer(64)
	payload := strings.Repeat("x", 64)
	if _, err := buf.Write([]byte(payload)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := buf.String(); got != payload {
		t.Fatalf("at-limit String() = %d bytes, want the full %d-byte payload retained", len(got), len(payload))
	}
	if got := buf.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d at the limit, want 0", got)
	}

	if _, err := buf.Write([]byte("y")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := buf.String(); got != payload {
		t.Fatalf("over-limit buffer must retain the first 64 bytes unchanged")
	}
	if got := buf.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d after one extra byte, want 1", got)
	}
}

func TestBoundedBufferUnderLimitRetainsVerbatim(t *testing.T) {
	t.Parallel()
	buf := newBoundedBuffer(64)
	if _, err := buf.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got, want := buf.String(), "hello"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got := buf.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d under the limit, want 0", got)
	}
}
