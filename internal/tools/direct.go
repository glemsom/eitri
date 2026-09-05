package tools

import (
	"context"
	"os"
)

// directRunner is the unsandboxed (--yolo-unsafe) bash backend: it executes a
// shell command directly as the current user — no bubblewrap cage is
// constructed — with the workspace as the working directory and the session
// temp as $TMPDIR, matching the sandboxed environment semantics minus the cage.
type directRunner struct {
	workspace string
	tempHost  string
	run       Runner
}

// Run executes the shell command cmd directly as the current user and returns
// its output. It mirrors the sandbox's environment contract (workspace cwd,
// session temp as TMPDIR/TEMP/TMP) while skipping the cage entirely.
func (d *directRunner) Run(ctx context.Context, cmd string) (*Output, error) {
	if err := os.MkdirAll(d.tempHost, 0o700); err != nil {
		return nil, err
	}
	return d.run.Run(ctx, RunSpec{
		Name: "/bin/bash",
		Args: []string{"-c", cmd},
		Dir:  d.workspace,
		Env:  []string{"TMPDIR=" + d.tempHost, "TEMP=" + d.tempHost, "TMP=" + d.tempHost},
	})
}

// setTempHost rewires the session temp directory exported as $TMPDIR; used when
// the registry binds a fresh per-session temp.
func (d *directRunner) setTempHost(tempHost string) {
	d.tempHost = tempHost
}
