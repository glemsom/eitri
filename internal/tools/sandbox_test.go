package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recordingRunner captures the exact argv handed to bwrap so tests can lock
// the sandbox construction (ADR-0001: host network, read-only root, workspace
// RW at host path, separate PID namespace, session temp as /tmp).
type recordingRunner struct {
	calls [][]string
	out   *Output
	err   error
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string) (*Output, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

// TestSandboxBuildsBwrapArgv locks the sandbox flag set by capturing the argv
// a fake runner receives for `bash`.
func TestSandboxBuildsBwrapArgv(t *testing.T) {
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	sb := NewSandbox("/home/u/proj", "/tmp/eitri-abc", rr)
	_, err := sb.Run(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	argv := rr.calls[0]
	// argv[0] must be bwrap.
	if argv[0] != "bwrap" {
		t.Fatalf("argv[0] = %q, want bwrap", argv[0])
	}
	want := []string{
		"--die-with-parent",
		"--share-net", // ADR-0001 decision 1: host network
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/dev/shm",
		"--bind", "/home/u/proj", "/home/u/proj",
		"--bind", "/tmp/eitri-abc", "/tmp",
		"--chdir", "/home/u/proj",
		"/bin/bash", "-c",
	}
	if len(argv) < len(want)+1 {
		t.Fatalf("argv too short: %v", argv)
	}
	for i, w := range want {
		if argv[i+1] != w { // argv[0] is bwrap
			t.Fatalf("argv[%d] = %q, want %q (argv=%v)", i+1, argv[i+1], w, argv)
		}
	}
	last := argv[len(argv)-1]
	if last != "echo hi" {
		t.Fatalf("last argv = %q, want command %q", last, "echo hi")
	}
}

// TestSandboxRunPropagatesOutput checks the runner result surfaces to the
// caller.
func TestSandboxRunPropagatesOutput(t *testing.T) {
	rr := &recordingRunner{out: &Output{Stdout: "hello\n", Stderr: "warn\n"}}
	sb := NewSandbox("/ws", "/tmp/eitri-z", rr)
	o, err := sb.Run(context.Background(), "ls")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if o.Stdout != "hello\n" || o.Stderr != "warn\n" {
		t.Fatalf("output = %+v, want stdout=hello stderr=warn", o)
	}
}

// TestSandboxRunPropagatesError verifies a failing command surfaces the error.
func TestSandboxRunPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	rr := &recordingRunner{err: sentinel}
	sb := NewSandbox("/ws", "/tmp/eitri-z", rr)
	_, err := sb.Run(context.Background(), "false")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want sentinel", err)
	}
}

// TestSandboxRealBwrapIntegration runs an actual bash command inside the real
// bubblewrap cage, verifying the workspace is writable and /tmp is remapped to
// the session temp. Skipped when sudo-less CI lacks bwrap; our dev host has it.
func TestSandboxRealBwrapIntegration(t *testing.T) {
	if !bwrapAvailable() {
		t.Skip("bwrap not present; skipping real sandbox test")
	}
	// The workspace must NOT live under /tmp, because the sandbox remaps /tmp to
	// the session temp; an in-/tmp workspace path would be shadowed in-cage. Use a
	// base dir under the user's home to mirror a real project path.
	ws := newNonRemappedWorkspace(t)
	tempHost := t.TempDir()
	sb := NewSandbox(ws, tempHost, defaultRunner{})
	// Workspace writable + /tmp remapped to the session temp host dir.
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
	// Session temp remap: write to /tmp inside the sandbox -> host tempHost dir.
	if _, err := sb.Run(context.Background(), "echo tmp-data > /tmp/inside.tmp"); err != nil {
		t.Fatalf("tmp write error = %v", err)
	}
	if _, err := os.Stat(tempHost + "/inside.tmp"); err != nil {
		t.Fatalf("sandbox /tmp write did not land in session temp host dir: %v", err)
	}
	// Fresh procfs: PID 1 in the pid namespace is the bwrap supervisor (the
	// in-ns reaper), never the host's PID 1 (systemd here).
	if _, err := sb.Run(context.Background(), "test \"$(cat /proc/1/comm)\" = bwrap || exit 1"); err != nil {
		t.Fatalf("sandbox /proc is not pid-namespace-scoped: %v", err)
	}
	// devtmpfs: device nodes exist and are not host devices.
	if _, err := sb.Run(context.Background(), "test -c /dev/null && test -c /dev/zero || exit 1"); err != nil {
		t.Fatalf("sandbox /dev lacks devtmpfs device nodes: %v", err)
	}
	// Private writable /dev/shm.
	if _, err := sb.Run(context.Background(), "touch /dev/shm/shm-probe && test -f /dev/shm/shm-probe || exit 1"); err != nil {
		t.Fatalf("sandbox /dev/shm not writable: %v", err)
	}
}

// newNonRemappedWorkspace creates a workspace directory under the user's home
// (not /tmp), so it survives the sandbox's /tmp remap at the same path in-cage.
func newNonRemappedWorkspace(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-test-ws-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	return ws
}

// bwrapAvailable reports whether the real bubblewrap binary exists on PATH.
func bwrapAvailable() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

var _ = os.Getenv
