// Package sandbox wraps shell commands inside a bubblewrap sandbox.
//
// It provides BwrapIsUsable to check whether bwrap is both installed and
// functional (can create user namespaces), BwrapAvailable which caches
// that result for the process lifetime, and WrapCommand which takes a
// command line and a config profile, and returns the executable and
// arguments to pass to exec.Command. If bwrap is not installed, not
// usable, or the profile is "none", the command is returned unchanged
// (direct bash -c).
//
// The default profile uses --ro-bind / / to make the entire filesystem
// read-only, then punches writable holes for the workspace and /tmp.
// Network is enabled by default; disable with Config.Network=false.
// Additional writable paths can be added via Config.ExtraWritablePaths.
package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Profile identifies a sandboxing profile.
type Profile string

const (
	// ProfileNone runs the command directly without any sandbox.
	ProfileNone Profile = "none"
	// ProfileDefault runs the command inside a bwrap sandbox with
	// read-only root, writable workspace and /tmp, and separate
	// PID namespace.
	ProfileDefault Profile = "default"
)

// Config controls sandbox behaviour.
type Config struct {
	Profile            Profile  `json:"profile"`
	Network            bool     `json:"network"`
	ExtraWritablePaths []string `json:"extra_writable_paths,omitempty"`
}

// DefaultConfig returns a Config with ProfileDefault and network enabled.
func DefaultConfig() Config {
	return Config{
		Profile: ProfileDefault,
		Network: true,
	}
}

// IsZero reports whether cfg is the zero value, meaning no explicit
// sandbox configuration was provided.
func IsZero(cfg Config) bool {
	return cfg.Profile == "" && !cfg.Network && len(cfg.ExtraWritablePaths) == 0
}

// BwrapIsUsable checks whether bwrap is installed and can actually create
// a sandbox. It runs "bwrap --ro-bind / / true" and returns true only if
// the command succeeds. This is more reliable than LookPath because bwrap
// may be installed (e.g. via apt on GitHub Actions) but fail at runtime
// due to missing user namespace support.
func BwrapIsUsable() bool {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	cmd := exec.Command(bwrap, "--ro-bind", "/", "/", "true")
	return cmd.Run() == nil
}

var bwrapAvailableCached = sync.OnceValue(bwrapIsUsableUncached)

// bwrapPathCached caches the result of exec.LookPath("bwrap") so WrapCommand
// doesn't search PATH on every invocation.
var bwrapPathCached = sync.OnceValues(func() (string, error) {
	return exec.LookPath("bwrap")
})

func bwrapIsUsableUncached() bool {
	return BwrapIsUsable()
}

// BwrapAvailable returns whether bwrap is usable, caching the result so
// the probe runs at most once per process lifetime.
func BwrapAvailable() bool {
	return bwrapAvailableCached()
}

// cleanup is a no-op fallback that can be returned when no cleanup is needed.
func nopCleanup() {}

// WrapCommand returns the executable path, argument list, and a cleanup
// function that the caller should defer. When sandboxing is active the
// returned executable is bwrap and the arguments include the full sandbox
// specification; otherwise the returned executable is "bash" with
// ["-c", command].
//
// The function creates an ephemeral temporary directory under /tmp that
// is mounted as /tmp inside the sandbox. The returned cleanup function
// removes this directory and logs at warn level on failure. The cleanup
// is idempotent — safe to call even if the directory was already removed.
//
// If bwrap is not found on PATH, or is found but not usable (e.g. due to
// missing user namespace support), the function falls back to direct
// execution, cleans up the already-created tmpdir, and returns a no-op
// cleanup so callers can always defer it.
func WrapCommand(workspace, command string, cfg Config) (string, []string, func(), error) {
	// Normalise zero config to defaults.
	if cfg.Profile == "" {
		cfg = DefaultConfig()
	}

	// Create ephemeral temp dir early so we can clean up on fallback paths.
	tmpDir, err := os.MkdirTemp("/tmp", "eitri-sandbox-*")
	if err != nil {
		return "", nil, nopCleanup, fmt.Errorf("sandbox: creating ephemeral tmp dir: %w", err)
	}

	// If MkdirTemp succeeded we need to clean up unless we return the real cleanup.
	// cleanupTmp removes tmpDir and logs at warn level on failure.
	// Retries up to 3 times with 50ms backoff to handle stale bwrap mount
	// references that can transiently cause EACCES on unlinkat.
	cleanupTmp := func() {
		var err error
		for range 3 {
			err = os.RemoveAll(tmpDir)
			if err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		slog.Warn("sandbox: failed to clean up ephemeral tmp dir",
			"path", tmpDir,
			"error", err,
		)
	}

	if cfg.Profile == ProfileNone {
		cleanupTmp()
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if runtime.GOOS != "linux" {
		cleanupTmp()
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if workspace == "" {
		cleanupTmp()
		return "", nil, nopCleanup, fmt.Errorf("sandbox: workspace is required for sandboxed execution")
	}

	bwrap, err := bwrapPathCached()
	if err != nil {
		slog.Debug("bwrap not found on PATH, running command without sandbox",
			slog.String("workspace", workspace),
		)
		cleanupTmp()
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if !BwrapAvailable() {
		slog.Debug("bwrap found on PATH but not usable (likely no user namespace support), running command without sandbox",
			slog.String("workspace", workspace),
		)
		cleanupTmp()
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	// Build bwrap arguments.
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
	}
	if !cfg.Network {
		args = append(args, "--unshare-net")
	}
	args = append(args,
		"--ro-bind", "/", "/",
		"--bind", workspace, workspace,
		"--bind", tmpDir, "/tmp",
		"--dev", "/dev",
		"--proc", "/proc",
	)

	// Add extra user-specified writable mounts.
	for _, p := range cfg.ExtraWritablePaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		args = append(args, "--bind", p, p)
	}

	args = append(args,
		"--chdir", workspace,
		"--", "bash", "-c", command,
	)

	return bwrap, args, cleanupTmp, nil
}
