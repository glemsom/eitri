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

// Output is the result of a sandboxed command: separated stdout/stderr so callers can decide how to combine them (the bash tool returns combined output for token efficiency).
type Output struct {
	Stdout string
	Stderr string
}

// Runner is the system-boundary seam that actually executes a command (bwrap in production).
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

// Sandbox runs shell commands inside the bubblewrap cage.
const sshConfigDirName = "etc-ssh-config.d"

type Sandbox struct {
	workspace     string
	tempHost      string
	extraWritable []string
	run           Runner
}

// NewSandbox builds a sandbox for workspace (host path, RW) with the session temp at tempHost (same absolute path inside and outside the cage). run is the command runner seam.
func NewSandbox(workspace, tempHost string, run Runner, extraWritable ...string) *Sandbox {
	if tempHost != "" {
		tempHost = filepath.Clean(tempHost)
	}
	return &Sandbox{workspace: workspace, tempHost: tempHost, extraWritable: cleanPaths(extraWritable), run: run}
}

// Run executes the shell command cmd inside the bwrap cage and returns its output. cmd is a shell string executed by /bin/bash -c.
func (s *Sandbox) Run(ctx context.Context, cmd string) (*Output, error) {
	if s.tempHost == "" {
		return nil, errors.New("session temp path is empty")
	}
	if err := os.MkdirAll(s.tempHost, 0o700); err != nil {
		return nil, err
	}
	if err := s.prepareSshConfig(); err != nil {
		return nil, err
	}
	args := []string{
		"--die-with-parent",
		"--share-net", // host network
		"--unshare-pid",
		"--ro-bind", "/", "/",
		"--ro-bind", filepath.Join(s.tempHost, sshConfigDirName), "/etc/ssh/ssh_config.d",
		"--proc", "/proc", // fresh procfs scoped to the pid namespace
		"--dev", "/dev", // devtmpfs replaces the ro-bind host /dev
		"--tmpfs", "/dev/shm", // devtmpfs has no /dev/shm; private writable shm
		"--bind", s.workspace, s.workspace,
		"--bind", s.tempHost, s.tempHost,
	}
	for _, p := range s.extraWritable {
		args = append(args, "--bind", p, p)
	}
	args = append(args,
		"--setenv", "TMPDIR", s.tempHost,
		"--setenv", "TEMP", s.tempHost,
		"--setenv", "TMP", s.tempHost,
		"--chdir", s.workspace,
		"/bin/bash", "-c", cmd,
	)
	return s.run.Run(ctx, "bwrap", args)
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// prepareSshConfig materializes a user-owned copy of /etc/ssh/ssh_config.d into the session temp (idempotently), dereferencing symlinks so referenced include files (e.g. /usr/lib/systemd/ssh_config.d/20-systemd-ssh-proxy.conf) become real files owned by the caller instead of host-root targets that read as nobody inside the user namespace.
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

// resolveRegularFile resolves src to the path of the regular file to copy, dereferencing symlinks so a symlink target rooted in /usr/lib/systemd is copied as a real, caller-owned file rather than a pointer to a host-root, nobody-in-cage target. ok is false when src does not map to a regular file, in which case the caller should skip the entry.
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
