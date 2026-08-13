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

// TestTUIBootErrorPipedStdout asserts the guard refuses the TUI when stdout is
// not a TTY (T7 AC1): the refusal error wraps ErrTUINotInteractive and its
// message directs the user to batch mode (-b).
func TestTUIBootErrorPipedStdout(t *testing.T) {
	err := tuiBootError(tuiEnv{stdoutTTY: false, term: "xterm-256color", width: 120})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("tuiBootError(piped) = %v, want ErrTUINotInteractive", err)
	}
	if !strings.Contains(err.Error(), "-b") {
		t.Fatalf("refusal message %q does not direct the user to batch mode (-b)", err.Error())
	}
}

// TestTUIBootErrorUnsetTerm asserts the guard refuses the TUI when TERM is
// unset (T7 AC2).
func TestTUIBootErrorUnsetTerm(t *testing.T) {
	err := tuiBootError(tuiEnv{stdoutTTY: true, term: "", width: 120})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("tuiBootError(unset TERM) = %v, want ErrTUINotInteractive", err)
	}
}

// TestTUIBootErrorDumbTerm asserts the guard refuses the TUI when TERM is
// "dumb" (T7 AC2).
func TestTUIBootErrorDumbTerm(t *testing.T) {
	err := tuiBootError(tuiEnv{stdoutTTY: true, term: "dumb", width: 120})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("tuiBootError(dumb TERM) = %v, want ErrTUINotInteractive", err)
	}
}

// TestTUIBootErrorNarrowTerminal asserts the guard refuses the TUI below the
// minimum width threshold (T7 AC3): 79 columns refuses, 80 columns passes.
func TestTUIBootErrorNarrowTerminal(t *testing.T) {
	err := tuiBootError(tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 79})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("tuiBootError(width 79) = %v, want ErrTUINotInteractive", err)
	}
	if !strings.Contains(err.Error(), "-b") {
		t.Fatalf("refusal message %q does not direct the user to batch mode (-b)", err.Error())
	}
	if err := tuiBootError(tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 80}); err != nil {
		t.Fatalf("tuiBootError(width 80) = %v, want nil at the threshold", err)
	}
}

// TestTUIBootErrorInteractiveTerminalOK asserts the guard lets a normal
// interactive launch through: TTY stdout, a real TERM, and a wide-enough
// window (T7 AC4).
func TestTUIBootErrorInteractiveTerminalOK(t *testing.T) {
	if err := tuiBootError(interactiveEnv); err != nil {
		t.Fatalf("tuiBootError(interactive) = %v, want nil", err)
	}
}

// TestTUIBootErrorUnknownWidthOK asserts an unknown width (0) does not refuse
// the TUI: the size probe is best-effort and must never block a real TTY
// launch when it fails.
func TestTUIBootErrorUnknownWidthOK(t *testing.T) {
	if err := tuiBootError(tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 0}); err != nil {
		t.Fatalf("tuiBootError(unknown width) = %v, want nil", err)
	}
}

// TestRunTUIRefusedWhenStdoutPiped drives the boot seam: with stdout not a
// TTY, Run must refuse the TUI with ErrTUINotInteractive and never launch the
// interactive program (T7 AC1 — no TUI reflow into the pipe).
func TestRunTUIRefusedWhenStdoutPiped(t *testing.T) {
	stubTUIEnv(t, tuiEnv{stdoutTTY: false, term: "xterm-256color", width: 120})
	launched := recordingTUI(t)
	dir := t.TempDir()

	err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: okLookPath})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("Run(piped) error = %v, want ErrTUINotInteractive", err)
	}
	if *launched {
		t.Fatal("Run(piped) launched the TUI; it must refuse before the program starts")
	}
}

// TestRunTUIRefusedWhenDumbTerm drives the boot seam: TERM=dumb refuses the
// TUI (T7 AC2).
func TestRunTUIRefusedWhenDumbTerm(t *testing.T) {
	stubTUIEnv(t, tuiEnv{stdoutTTY: true, term: "dumb", width: 120})
	launched := recordingTUI(t)
	dir := t.TempDir()

	err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: okLookPath})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("Run(dumb TERM) error = %v, want ErrTUINotInteractive", err)
	}
	if *launched {
		t.Fatal("Run(dumb TERM) launched the TUI; it must refuse before the program starts")
	}
}

// TestRunTUIRefusedWhenNarrow drives the boot seam: a sub-threshold terminal
// width refuses the TUI (T7 AC3).
func TestRunTUIRefusedWhenNarrow(t *testing.T) {
	stubTUIEnv(t, tuiEnv{stdoutTTY: true, term: "xterm-256color", width: 60})
	launched := recordingTUI(t)
	dir := t.TempDir()

	err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: okLookPath})
	if !errors.Is(err, ErrTUINotInteractive) {
		t.Fatalf("Run(narrow) error = %v, want ErrTUINotInteractive", err)
	}
	if *launched {
		t.Fatal("Run(narrow) launched the TUI; it must refuse before the program starts")
	}
}

// TestRunTUIProceedsWhenInteractive drives the boot seam: a normal interactive
// context still enters the full-screen TUI (T7 AC4).
func TestRunTUIProceedsWhenInteractive(t *testing.T) {
	stubTUIEnv(t, interactiveEnv)
	launched := recordingTUI(t)
	dir := t.TempDir()

	if err := Run(Options{DataDir: filepath.Join(dir, ".eitri"), LookPath: okLookPath}); err != nil {
		t.Fatalf("Run(interactive) error = %v, want nil", err)
	}
	if !*launched {
		t.Fatal("Run(interactive) never launched the TUI")
	}
}

// TestRunBatchUnaffectedByNonInteractiveEnv asserts the refusal lives on the
// interactive entrant only: batch mode (-b) still runs end to end even when
// the host context is non-interactive (T7 AC5).
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
