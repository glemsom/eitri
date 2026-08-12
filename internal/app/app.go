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
)

// Version reports the Eitri build version tag, set at build time.
var Version = "0.1.0-dev"

// Environment variables honored at boot (eitri.md §2.7).
const (
	// DataDirEnv overrides the default ~/.eitri data directory.
	DataDirEnv = "EITRI_DIR"
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

	// LookPath locates an executable on the host PATH. It defaults to
	// exec.LookPath; tests inject a stub to drive bwrap-missing behavior.
	LookPath func(name string) (string, error)
}

// Run performs the Eitri boot sequence and returns the first error it hits, so
// a caller can map it to an exit status. The workspace, session temp, sandbox
// (bwrap cage), host-side tools, and path namespace are later components that
// hang off this sequence.
func Run(opts Options) error {
	// The prompt (-b) and debug (-d) flags are parsed at the CLI layer but their
	// engine behavior is wired in later tickets; boot treats them as no-ops.
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

	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("bwrap"); err != nil {
		return ErrMissingBwrap
	}

	return nil
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
