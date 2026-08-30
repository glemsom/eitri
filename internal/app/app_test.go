package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/tui"
)

func stubTUI(t *testing.T) {
	t.Helper()
	stubTUIEnv(t, interactiveEnv)
	orig := runProgram
	runProgram = func(m tui.Model) error { return nil }
	t.Cleanup(func() { runProgram = orig })
}

func TestRunCreatesDataDir(t *testing.T) {
	stubTUI(t)
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
	stubTUI(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := Run(Options{DataDir: dataDir, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run() error = %v, want nil when data dir already exists", err)
	}
}

func TestRunErrorsWithoutDeclaredDependencies(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	err := Run(Options{DataDir: dataDir, LookPath: missingLookPath})
	if err == nil {
		t.Fatal("Run() error = nil, want a fatal missing-dependencies error")
	}
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run() error = %v, want ErrMissingDependencies", err)
	}
	errMsg := err.Error()
	for _, name := range declaredDependencyNames() {
		if !contains(errMsg, name) {
			t.Fatalf("Run() error %q does not name the missing tool %q", errMsg, name)
		}
	}
	if !containsInstallMsg(errMsg) {
		t.Fatalf("Run() error %q does not contain an install hint", errMsg)
	}
}

func TestRunRefusesBootWhenBwrapAloneIsMissing(t *testing.T) {
	dir := t.TempDir()
	present := []string{"bash", "rg", "curl", "lynx", "patch", "python3", "git"}
	err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: lookup(present...)})
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run() error = %v, want ErrMissingDependencies; bwrap absence must stay a hard failure inside the single check pass", err)
	}
	de, ok := err.(*DependencyError)
	if !ok {
		t.Fatalf("error type = %T, want *DependencyError", err)
	}
	if strings.Join(de.Missing, ",") != "bwrap" {
		t.Fatalf("DependencyError.Missing = %v, want exactly [bwrap]", de.Missing)
	}
}

func TestVersionShortCircuitsBoot(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	if err := Run(Options{Version: true, DataDir: dataDir, LookPath: missingLookPath}); err != nil {
		t.Fatalf("Run(version) error = %v, want nil", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Run(version) should not boot the data dir, stat err = %v", err)
	}
}

func TestBootLoadsConfigAndCreatesSession(t *testing.T) {
	stubTUI(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	cfgPath := filepath.Join(dir, "config.json")

	if err := Run(Options{DataDir: dataDir, ConfigPath: cfgPath, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not created at %s: %v", cfgPath, err)
	}
	var got struct {
		MaxTurns int `json:"max_turns"`
	}
	if err := json.Unmarshal(cfgData, &got); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if got.MaxTurns != 250 {
		t.Fatalf("config MaxTurns = %d, want default 250", got.MaxTurns)
	}

	sessions := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(sessions)
	if err != nil {
		t.Fatalf("sessions dir %s not created: %v", sessions, err)
	}
	if len(entries) != 1 {
		t.Fatalf("sessions dir has %d entries, want exactly 1 session", len(entries))
	}
	if !entries[0].IsDir() {
		t.Fatalf("sessions/%s is not a directory", entries[0].Name())
	}
}

func TestBootDebugCreatesTraceCapableSession(t *testing.T) {
	stubTUI(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	if err := Run(Options{DataDir: dataDir, Debug: true, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run(debug) error = %v, want nil", err)
	}
	sessions := filepath.Join(dataDir, "sessions")
	if _, err := os.Stat(sessions); err != nil {
		t.Fatalf("sessions dir %s not created in debug mode: %v", sessions, err)
	}
}

func TestBootUsesDataDirForConfig(t *testing.T) {
	stubTUI(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	if err := Run(Options{DataDir: dataDir, LookPath: okLookPath}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "config.json")); err != nil {
		t.Fatalf("config not at dataDir/config.json: %v", err)
	}
}

var okLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

var missingLookPath = func(name string) (string, error) { return "", errors.New("executable not found") }

func containsInstallMsg(s string) bool {
	return len(s) > 0 && (contains(s, "sudo apt install") || contains(s, "sudo dnf install") || contains(s, "sudo pacman -S"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
