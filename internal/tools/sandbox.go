package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
// separate PID namespace, and host network (--share-net). A fresh procfs is
// mounted on /proc (scoped to the pid namespace), a devtmpfs on /dev, and a
// private tmpfs on /dev/shm, so the sandbox sees its own process table and
// device tree instead of the host's. It never falls back to unsandboxed
// execution.
// sshConfigDirName is the name of the sub-directory of the session temp that is
// bound read-only over the in-cage mount destination /etc/ssh/ssh_config.d
// (issue #271). It maps 1:1 onto the system config path so the sanitized,
// user-owned copy shadows the host-root originals, keeping OpenSSH's strict
// ownership check on the include files from failing inside the user-namespace
// cage.
const sshConfigDirName = "etc-ssh-config.d"

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
	if err := s.prepareSshConfig(); err != nil {
		return nil, err
	}
	args := []string{
		"--die-with-parent",
		"--share-net", // ADR-0001 decision 1: host network
		"--unshare-pid",
		"--ro-bind", "/", "/",
		// Issue #271: host-root /etc/ssh/* presents as nobody (uid 65534) inside
		// the unprivileged user-namespace cage, so OpenSSH's ownership check on
		// the include files fails. Shadow the system config with a sanitized,
		// user-owned copy mounted read-only AFTER the root bind.
		"--ro-bind", filepath.Join(s.tempHost, sshConfigDirName), "/etc/ssh/ssh_config.d",
		"--proc", "/proc", // fresh procfs scoped to the pid namespace
		"--dev", "/dev", // devtmpfs replaces the ro-bind host /dev
		"--tmpfs", "/dev/shm", // devtmpfs has no /dev/shm; private writable shm
		"--bind", s.workspace, s.workspace,
		"--bind", s.tempHost, "/tmp",
		"--chdir", s.workspace,
		"/bin/bash", "-c", cmd,
	}
	return s.run.Run(ctx, "bwrap", args)
}

// prepareSshConfig materializes a user-owned copy of /etc/ssh/ssh_config.d into
// the session temp (idempotently), dereferencing symlinks so referenced include
// files (e.g. /usr/lib/systemd/ssh_config.d/20-systemd-ssh-proxy.conf) become
// real files owned by the caller instead of host-root targets that read as
// nobody inside the user namespace. This keeps `ssh -G` and git-over-ssh
// working while never running the sandbox setuid (issue #271).
func (s *Sandbox) prepareSshConfig() error {
	src := "/etc/ssh/ssh_config.d"
	dst := filepath.Join(s.tempHost, sshConfigDirName)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil // no system drop-ins to mirror; leave the empty dir bound
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // ssh_config.d holds only .conf files; skip nested dirs
		}
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		target, ok, err := resolveRegularFile(sp)
		if err != nil {
			return err // real I/O error, not a mere absent entry
		}
		if !ok {
			continue // not a regular file (missing target, non-regular); skip
		}
		if err := copyFile(target, dp); err != nil {
			return err
		}
	}
	return nil
}

// resolveRegularFile resolves src to the path of the regular file to copy,
// dereferencing symlinks so a symlink target rooted in /usr/lib/systemd is
// copied as a real, caller-owned file rather than a pointer to a host-root,
// nobody-in-cage target. ok is false when src does not map to a regular file,
// in which case the caller should skip the entry. A genuine I/O error is
// returned as err: a vanished entry (os.ErrNotExist) counts as skip, not
// error, while any other failure (permission, etc.) propagates so the caller
// surfaces it instead of silently failing open.
func resolveRegularFile(src string) (path string, ok bool, err error) {
	info, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil // vanished between readdir and lstat; skip
		}
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return "", false, err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(src), target)
		}
		return resolveRegularFile(target) // deref chained symlinks
	}
	if !info.Mode().IsRegular() {
		return "", false, nil // non-regular (dir, socket, ...); skip
	}
	return src, true, nil
}

// copyFile copies src to dst, creating dst as a plain caller-owned file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
