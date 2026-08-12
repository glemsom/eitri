// Package app drives the Eitri boot sequence: resolving the data directory,
// checking the bubblewrap (bwrap) sandbox prerequisite, and wiring flag-driven
// behavior. Later tickets hang the run engine, config, and storage off this
// boot path.
package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// Version reports the Eitri build version tag, set at build time.
var Version = "0.1.0-dev"

// Environment variables honored at boot (eitri.md §2.7).
const (
	// DataDirEnv overrides the default ~/.eitri data directory.
	DataDirEnv = "EITRI_DIR"
	// ConfigEnv overrides the default config file path (~/.eitri/config.json).
	ConfigEnv = "EITRI_CONFIG"
)

// ErrMissingBwrap is returned when the bubblewrap (bwrap) executable cannot be
// found on the host. Per ADR-0001 decision 3, bwrap is a hard prerequisite:
// Eitri never falls back to unsandboxed execution.
var ErrMissingBwrap = errors.New("bubblewrap (bwrap) is required but was not found; install bubblewrap to continue")

// Options control a single Run invocation.
type Options struct {
	// Version, when true, prints the version and exits without booting.
	Version bool

	// DataDir is the top-level data directory. When empty, it is resolved from
	// EITRI_DIR or defaults to ~/.eitri.
	DataDir string

	// ConfigPath is the config file path. When empty, it is resolved from
	// EITRI_DIR/config.json (or EITRI_CONFIG when that is set).
	ConfigPath string

	// Debug enables debug mode (-d), attaching the HTTP trace sink to the run
	// session for deep-dive provider debugging (eitri.md §2.5).
	Debug bool

	// LookPath locates an executable on the host PATH. It defaults to
	// exec.LookPath; tests inject a stub to drive bwrap-missing behavior.
	LookPath func(name string) (string, error)
}

// Run performs the Eitri boot sequence and returns the first error it hits, so
// a caller can map it to an exit status. The workspace, session temp, sandbox
// (bwrap cage), host-side tools, and path namespace are later components that
// hang off this sequence.
func Run(opts Options) error {
	// The prompt (-b) flag is parsed at the CLI layer but its engine behavior
	// is wired in a later ticket (T1c); boot treats it as a no-op. The debug
	// (-d) flag is honored here via opts.Debug when establishing the session.
	if opts.Version {
		fmt.Println(Version)
		return nil
	}

	dir, err := resolveDataDir(opts.DataDir)
	if err != nil {
		return err
	}
	if err := ensureDataDir(dir); err != nil {
		return err
	}

	cfgPath, err := resolveConfigPath(dir, opts.ConfigPath)
	if err != nil {
		return err
	}
	if _, err := config.Load(cfgPath); err != nil {
		return err
	}

	// Establish the run's on-disk session trail under sessions/<GUID>.
	if _, err := session.New(dir, opts.Debug); err != nil {
		return err
	}

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("bwrap"); err != nil {
		return ErrMissingBwrap
	}

	return nil
}

// resolveConfigPath selects the config file path: an explicit override, else
// EITRI_CONFIG, else <dataDir>/config.json.
func resolveConfigPath(dataDir, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(ConfigEnv); env != "" {
		return env, nil
	}
	return filepath.Join(dataDir, "config.json"), nil
}

// resolveDataDir selects the data directory: the explicit override, else
// EITRI_DIR, else ~/.eitri.
func resolveDataDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv(DataDirEnv); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("resolve data dir: cannot determine home directory")
	}
	return filepath.Join(home, ".eitri"), nil
}

// ensureDataDir creates the data directory, tolerating its pre-existence.
func ensureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("create data dir " + dir + ": " + err.Error())
	}
	return nil
}
