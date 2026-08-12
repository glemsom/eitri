package tools

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

// Output is the result of a sandboxed command: separated stdout/stderr so
// callers can decide how to combine them (the bash tool returns combined
// output for token efficiency).
type Output struct {
	Stdout string
	Stderr string
}

// Runner is the system-boundary seam that actually executes a command (bwrap
// in production). It is injectable so sandbox construction is testable without
// spawning processes; the real implementation is defaultRunner.
type Runner interface {
	Run(ctx context.Context, name string, args []string) (*Output, error)
}

// RealRunner is the production command runner that actually spawns bwrap.
var RealRunner Runner = defaultRunner{}

// defaultRunner executes name with args and captures combined output.
type defaultRunner struct{}

func (defaultRunner) Run(ctx context.Context, name string, args []string) (*Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return &Output{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

// Sandbox runs shell commands inside the bubblewrap cage (ADR-0001). It is a
// defense-in-depth boundary: root mounted read-only, the workspace mounted
// read-write at its host path, the session temp mounted as sandbox /tmp, a
// separate PID namespace, and host network (--share-net). It never falls back
// to unsandboxed execution.
type Sandbox struct {
	workspace string
	tempHost  string
	run       Runner
}

// NewSandbox builds a sandbox for workspace (host path, RW) with the session
// temp at tempHost (host /tmp/eitri-<GUID>). run is the command runner seam.
func NewSandbox(workspace, tempHost string, run Runner) *Sandbox {
	return &Sandbox{workspace: workspace, tempHost: tempHost, run: run}
}

// Run executes the shell command cmd inside the bwrap cage and returns its
// output. cmd is a shell string executed by /bin/bash -c.
func (s *Sandbox) Run(ctx context.Context, cmd string) (*Output, error) {
	// The session temp host root must exist: bwrap refuses to bind a missing
	// source (ADR-0001/0002). Create it idempotently so the sandbox /tmp
	// depends on a real, writable host dir.
	if err := os.MkdirAll(s.tempHost, 0o700); err != nil {
		return nil, err
	}
	args := []string{
		"--die-with-parent",
		"--share-net", // ADR-0001 decision 1: host network
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--bind", s.workspace, s.workspace,
		"--bind", s.tempHost, "/tmp",
		"--chdir", s.workspace,
		"/bin/bash", "-c", cmd,
	}
	return s.run.Run(ctx, "bwrap", args)
}
