// Package sandbox wraps shell commands inside a bubblewrap sandbox.
//
// It provides a single function, WrapCommand, that takes a command line
// and a config profile, and returns the executable and arguments to pass
// to exec.Command. If bwrap is not installed or the profile is "none",
// the command is returned unchanged (direct bash -c).
//
// The default profile uses --ro-bind / / to make the entire filesystem
// read-only, then punches writable holes for the workspace and /tmp.
// Network is enabled by default; disable with Config.Network=false.
// Additional writable paths can be added via Config.ExtraWritablePaths.
package sandbox

import (
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
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

// WrapCommand returns the executable path and argument list that the
// caller should pass to exec.Command. When sandboxing is active the
// returned executable is bwrap and the arguments include the full
// sandbox specification; otherwise the returned executable is "bash"
// with ["-c", command].
//
// If bwrap is not found on PATH the function falls back to direct
// execution and logs a warning at debug level.
func WrapCommand(workspace, command string, cfg Config) (string, []string, error) {
	// Normalise zero config to defaults.
	if cfg.Profile == "" {
		cfg = DefaultConfig()
	}

	if cfg.Profile == ProfileNone || cfg.Profile == "" {
		return "bash", []string{"-c", command}, nil
	}

	if runtime.GOOS != "linux" {
		return "bash", []string{"-c", command}, nil
	}

	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		slog.Debug("bwrap not found on PATH, running command without sandbox",
			slog.String("workspace", workspace),
		)
		return "bash", []string{"-c", command}, nil
	}

	if workspace == "" {
		return "", nil, fmt.Errorf("sandbox: workspace is required for sandboxed execution")
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
		"--bind", "/tmp", "/tmp",
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

	return bwrap, args, nil
}
