package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCreatesDataDir(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	if err := Run(Options{DataDir: dataDir, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if fi, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir not created: %v", err)
	} else if !fi.IsDir() {
		t.Fatalf("data dir %s is not a directory", dataDir)
	}
}

func TestRunToleratesExistingDataDir(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := Run(Options{DataDir: dataDir, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run() error = %v, want nil when data dir already exists", err)
	}
}

func TestRunErrorsWithoutBwrap(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	err := Run(Options{DataDir: dataDir, LookPath: missingLookPath})
	if err == nil {
		t.Fatal("Run() error = nil, want an install-bubblewrap error")
	}
	if !errors.Is(err, ErrMissingBwrap) {
		t.Fatalf("Run() error = %v, want ErrMissingBwrap", err)
	}
	if !containsInstallMsg(err.Error()) {
		t.Fatalf("Run() error %q does not contain an install-bubblewrap hint", err.Error())
	}
}

func TestRunHonorsNoUnsandboxedFallback(t *testing.T) {
	// The absent-bwrap outcome must be a hard error, never a silent success.
	dir := t.TempDir()
	if err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: missingLookPath}); !errors.Is(err, ErrMissingBwrap) {
		t.Fatalf("Run() error = %v, want ErrMissingBwrap; bwrap absence must be a hard failure", err)
	}
}

func TestVersionShortCircuitsBoot(t *testing.T) {
	dir := t.TempDir()
	// Even with bwrap missing, --version must succeed and create no data dir.
	dataDir := filepath.Join(dir, ".eitri")
	if err := Run(Options{Version: true, DataDir: dataDir, LookPath: missingLookPath}); err != nil {
		t.Fatalf("Run(version) error = %v, want nil", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Run(version) should not boot the data dir, stat err = %v", err)
	}
}

var okLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

var missingLookPath = func(name string) (string, error) { return "", errors.New("executable not found") }

func containsInstallMsg(s string) bool {
	return len(s) > 0 && (contains(s, "bubblewrap") || contains(s, "bwrap"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
