package app

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tui"
)

// stubTUIEnv replaces the host-terminal facts seam with a fixed value, so boot
// tests drive the TUI refusal conditions without a real terminal (same pattern
// as stubTUI / runProgram).
func stubTUIEnv(t *testing.T, env tuiEnv) {
	t.Helper()
	orig := currentTUIEnv
	currentTUIEnv = func() tuiEnv { return env }
	t.Cleanup(func() { currentTUIEnv = orig })
}

// recordingTUI replaces the TUI program launcher with a recorder so a test can
// assert whether the interactive TUI was (not) launched.
func recordingTUI(t *testing.T) *bool {
	t.Helper()
	called := false
	orig := runProgram
	runProgram = func(m tui.Model) error { called = true; return nil }
	t.Cleanup(func() { runProgram = orig })
	return &called
}

// interactiveEnv is the host-terminal context a normal interactive launch sees.
var interactiveEnv = tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 120}

// TestTUIBootError tables the guard decision across every host-terminal context:
// stdout piped, unset/dumb TERM (any case, incl. dumb-* variants), sub-threshold
// width, the 80-column boundary, a healthy interactive launch, and an unknown
// width (0) which never refuses. Each refusal case must wrap
// ErrTUINotInteractive and direct the user to batch mode (-b).
func TestTUIBootError(t *testing.T) {
	tests := []struct {
		name    string
		env     tuiEnv
		wantErr bool
	}{
		{"piped stdout", tuiEnv{stdoutTTY: false, term: "xterm-256color", width: 120}, true},
		{"unset TERM", tuiEnv{stdoutTTY: true, term: "", width: 120}, true},
		{"dumb TERM", tuiEnv{stdoutTTY: true, term: "dumb", width: 120}, true},
		{"uppercase DUMB TERM", tuiEnv{stdoutTTY: true, term: "DUMB", width: 120}, true},
		{"dumb-16color TERM", tuiEnv{stdoutTTY: true, term: "dumb-16color", width: 120}, true},
		{"narrow terminal", tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 79}, true},
		{"threshold width", tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 80}, false},
		{"interactive terminal", interactiveEnv, false},
		{"unknown width", tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tuiBootError(tt.env)
			if tt.wantErr {
				if !errors.Is(err, ErrTUINotInteractive) {
					t.Fatalf("tuiBootError(%+v) = %v, want ErrTUINotInteractive", tt.env, err)
				}
				if !strings.Contains(err.Error(), "-b") {
					t.Fatalf("refusal message %q does not direct the user to batch mode (-b)", err.Error())
				}
			} else if err != nil {
				t.Fatalf("tuiBootError(%+v) = %v, want nil", tt.env, err)
			}
		})
	}
}

// TestRunTUIGuard drives the boot seam: Run must refuse the TUI (and never
// launch the interactive program) in every non-interactive context, and must
// still enter the TUI on a normal interactive launch.
func TestRunTUIGuard(t *testing.T) {
	tests := []struct {
		name        string
		env         tuiEnv
		wantRefused bool
	}{
		{"piped stdout", tuiEnv{stdoutTTY: false, term: "xterm-256color", width: 120}, true},
		{"dumb TERM", tuiEnv{stdoutTTY: true, term: "dumb", width: 120}, true},
		{"narrow terminal", tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 60}, true},
		{"interactive terminal", interactiveEnv, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubTUIEnv(t, tt.env)
			launched := recordingTUI(t)
			dir := t.TempDir()

			err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: okLookPath})
			if tt.wantRefused {
				if !errors.Is(err, ErrTUINotInteractive) {
					t.Fatalf("Run(%s) error = %v, want ErrTUINotInteractive", tt.name, err)
				}
				if *launched {
					t.Fatalf("Run(%s) launched the TUI; it must refuse before the program starts", tt.name)
				}
			} else {
				if err != nil {
					t.Fatalf("Run(%s) error = %v, want nil", tt.name, err)
				}
				if !*launched {
					t.Fatalf("Run(%s) never launched the TUI", tt.name)
				}
			}
		})
	}
}

// TestRunBatchUnaffectedByNonInteractiveEnv asserts the refusal lives on the
// interactive entrant only: batch mode (-b) still runs end to end even when
// the host context is non-interactive.
func TestRunBatchUnaffectedByNonInteractiveEnv(t *testing.T) {
	stubTUIEnv(t, tuiEnv{stdoutTTY: false, term: "", width: 0})
	dir := t.TempDir()
	var out bytes.Buffer

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch, non-interactive env) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "Hello world") {
		t.Fatalf("batch output %q missing the final answer", out.String())
	}
}
