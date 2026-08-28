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

type recordingRunner struct {
	calls [][]string
	out   *Output
	err   error
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string) (*Output, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

func TestSandboxBuildsBwrapArgv(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	tempHost := filepath.Join(t.TempDir(), "tmp")
	sb := NewSandbox("/home/u/proj", tempHost, rr, "/tmp/kubeconfig")
	_, err := sb.Run(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(rr.calls))
	}
	argv := rr.calls[0]
	if argv[0] != "bwrap" {
		t.Fatalf("argv[0] = %q, want bwrap", argv[0])
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
		if argv[i+1] != w { // argv[0] is bwrap
			t.Fatalf("argv[%d] = %q, want %q (argv=%v)", i+1, argv[i+1], w, argv)
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
	sb := NewSandbox("/ws", "/tmp/eitri-z", rr)
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
	sb := NewSandbox("/ws", "/tmp/eitri-z", rr)
	_, err := sb.Run(context.Background(), "false")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want sentinel", err)
	}
}

func TestSandboxRegistersSshConfigMount(t *testing.T) {
	t.Parallel()
	rr := &recordingRunner{out: &Output{Stdout: "ok"}}
	tempHost := t.TempDir()
	sb := NewSandbox("/ws", tempHost, rr)
	if _, err := sb.Run(context.Background(), "true"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	argv := rr.calls[0]
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
	sb := NewSandbox(ws, tempHost, defaultRunner{})
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
	sbWithExtra := NewSandbox(ws, tempHost, defaultRunner{}, extraTmpPath)
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

func bwrapAvailable() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var _ = os.Getenv
