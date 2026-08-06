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
//
// A Manager provides session-scoped /tmp persistence: for a given session
// ID, one host directory is mounted at /tmp for every sandboxed command of
// that session, so files written to /tmp survive across calls. The directory
// is created lazily on first use and removed by EndSession. Direct
// (sandbox-disabled) execution creates no session tmpdir.
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

// Manager owns session-scoped sandbox /tmp directories. A single Manager
// should be shared by all sandboxing consumers that want /tmp to persist
// across calls within the same run.
type Manager struct {
	config Config
	mu     sync.Mutex
	// tmpdirs maps a session ID to the host directory mounted at /tmp inside
	// that session's sandbox.
	tmpdirs map[string]string
}

// NewManager returns a Manager that sandboxes commands according to cfg.
// Zero-value configs are normalised to DefaultConfig on first use.
func NewManager(cfg Config) *Manager {
	return &Manager{
		config:  cfg,
		tmpdirs: make(map[string]string),
	}
}

// TmpdirFor returns the host path of the session's sandbox tmpdir and whether
// it is currently tracked. It is exported for tests and for tools that need to
// map sandbox /tmp paths back to the host.
func (m *Manager) TmpdirFor(sessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, ok := m.tmpdirs[sessionID]
	return dir, ok
}

// WrapCommand returns the executable path, argument list, and a cleanup
// function that the caller should defer. When sandboxing is active the
// returned executable is bwrap and the arguments include the full sandbox
// specification; otherwise the returned executable is "bash" with
// ["-c", command].
//
// When sessionID is non-empty the /tmp directory is session-scoped: created
// lazily on the first call for that session and reused for every subsequent
// call, so files written to /tmp persist across calls within the same run.
// The returned cleanup is a no-op in this case — the session tmpdir is removed
// by EndSession, not by each call.
//
// When sessionID is empty the /tmp directory is ephemeral (created and removed
// per call), preserving per-command isolation.
//
// If bwrap is not found on PATH, or is found but not usable (e.g. due to
// missing user namespace support), the function falls back to direct
// execution and returns a no-op cleanup so callers can always defer it.
func (m *Manager) WrapCommand(workspace, command, sessionID string) (string, []string, func(), error) {
	// Normalise zero config to defaults.
	cfg := m.config
	if cfg.Profile == "" {
		cfg = DefaultConfig()
	}

	if cfg.Profile == ProfileNone {
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if runtime.GOOS != "linux" {
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if workspace == "" {
		return "", nil, nopCleanup, fmt.Errorf("sandbox: workspace is required for sandboxed execution")
	}

	bwrap, err := bwrapPathCached()
	if err != nil {
		slog.Debug("bwrap not found on PATH, running command without sandbox",
			slog.String("workspace", workspace),
		)
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	if !BwrapAvailable() {
		slog.Debug("bwrap found on PATH but not usable (likely no user namespace support), running command without sandbox",
			slog.String("workspace", workspace),
		)
		return "bash", []string{"-c", command}, nopCleanup, nil
	}

	// Resolve the tmpdir to bind at /tmp.
	var tmpDir string
	var cleanup func()
	if sessionID == "" {
		// Per-command ephemeral tmpdir, removed when the returned cleanup runs.
		dir, mkErr := os.MkdirTemp("/tmp", "eitri-sandbox-*")
		if mkErr != nil {
			return "", nil, nopCleanup, fmt.Errorf("sandbox: creating ephemeral tmp dir: %w", mkErr)
		}
		tmpDir = dir
		cleanup = removeTmpdir(dir)
	} else {
		var err error
		tmpDir, err = m.getOrCreate(sessionID)
		if err != nil {
			return "", nil, nopCleanup, err
		}
		// Session tmpdir lives until EndSession; the per-call cleanup is a no-op.
		cleanup = nopCleanup
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

	return bwrap, args, cleanup, nil
}

// getOrCreate returns the session-scoped tmpdir for sessionID, creating and
// tracking it on first use.
func (m *Manager) getOrCreate(sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dir, ok := m.tmpdirs[sessionID]; ok {
		return dir, nil
	}
	// The session tmpdir path is deterministic so tools can map sandbox /tmp
	// paths back to the host (ADR-0026). Sanitise the session ID into a
	// path-safe component.
	dir, err := os.MkdirTemp("/tmp", "eitri-sandbox-"+pathSafe(sessionID)+"-")
	if err != nil {
		return "", fmt.Errorf("sandbox: creating session tmp dir: %w", err)
	}
	m.tmpdirs[sessionID] = dir
	return dir, nil
}

// EndSession removes the session-scoped tmpdir for sessionID, if any. It is
// idempotent and safe to call for unknown sessions.
func (m *Manager) EndSession(sessionID string) {
	m.mu.Lock()
	dir, ok := m.tmpdirs[sessionID]
	if ok {
		delete(m.tmpdirs, sessionID)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	removeTmpdir(dir)()
}

// pathSafe returns sessionID with characters that are not safe in a path
// component replaced by underscores.
func pathSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// removeTmpdir returns a cleanup func that removes dir, retrying transient
// EACCES from stale bwrap mount references, and logs at warn level on failure.
func removeTmpdir(dir string) func() {
	return func() {
		var err error
		for range 3 {
			err = os.RemoveAll(dir)
			if err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		slog.Warn("sandbox: failed to clean up tmp dir",
			"path", dir,
			"error", err,
		)
	}
}

// WrapCommand returns the executable path, argument list, and a cleanup
// function for running command inside a bubblewrap sandbox with config cfg.
// It is a compatibility wrapper around a per-command ephemeral /tmp (an empty
// session): each invocation's /tmp is isolated and removed by the returned
// cleanup. Session-scoped callers should use Manager.WrapCommand instead.
func WrapCommand(workspace, command string, cfg Config) (string, []string, func(), error) {
	return NewManager(cfg).WrapCommand(workspace, command, "")
}
