package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

func TestRunBatchReturnsAnswer(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "Hello world") {
		t.Fatalf("batch output %q missing the final answer", out.String())
	}
}

func TestRunBatchSuppressesThinkingByDefault(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if strings.Contains(out.String(), "think step by step") {
		t.Fatalf("reasoning leaked to stdout by default: %q", out.String())
	}
}

func TestRunBatchVerboseShowsThinking(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	dir := t.TempDir()

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Verbose:  true,
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch -v) error = %v, want nil", err)
	}
	if !strings.Contains(errOut.String(), "think step by step") {
		t.Fatalf("verbose reasoning did not stream to stderr: %q", errOut.String())
	}
	if strings.Contains(out.String(), "think step by step") {
		t.Fatalf("reasoning leaked to stdout: %q", out.String())
	}
	if strings.TrimSpace(out.String()) != "Hello world" {
		t.Fatalf("stdout = %q, want only the final answer", out.String())
	}
}

func TestRunRefusesWhenGitMissing(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	// git is now a declared dependency: its absence must refuse startup exactly
	// like any other declared tool, not degrade quietly.
	missingGit := func(name string) (string, error) {
		if name == "git" {
			return "", errors.New("executable not found: git")
		}
		return okLookPath(name)
	}

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: missingGit,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run() error = %v, want ErrMissingDependencies when git is missing", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when boot refuses on a missing declared dependency", out.String())
	}
}

func TestRunBatchRefusesWhenXDGOpenMissing(t *testing.T) {
	var out bytes.Buffer
	missingXDGOpen := func(name string) (string, error) {
		if name == "xdg-open" {
			return "", errors.New("executable not found: xdg-open")
		}
		return okLookPath(name)
	}

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: missingXDGOpen,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run() error = %v, want ErrMissingDependencies when xdg-open is missing", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when boot refuses on missing xdg-open", out.String())
	}
}

// lookPathExcept stubs the executable-lookup seam like okLookPath but reports
// the single named executable as absent, for boot-gate tests.
func lookPathExcept(missing string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name == missing {
			return "", errors.New("executable not found: " + name)
		}
		return okLookPath(name)
	}
}

func TestRunBatchYoloStartsWithoutBwrapOnPath(t *testing.T) {
	var out bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: lookPathExcept("bwrap"),
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
		Yolo:     true,
	})
	if err != nil {
		t.Fatalf("Run(yolo, no bwrap on PATH) error = %v, want nil", err)
	}
}

func TestRunBatchDefaultRefusesWithoutBwrapOnPath(t *testing.T) {
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: lookPathExcept("bwrap"),
		Prompt:   "Say hello",
		Stdout:   &bytes.Buffer{},
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run(default, no bwrap on PATH) error = %v, want ErrMissingDependencies", err)
	}
}

func TestRunBatchWritesTranscript(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &bytes.Buffer{},
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	sessions := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(sessions)
	if err != nil {
		t.Fatalf("sessions dir %s not created: %v", sessions, err)
	}
	if len(entries) != 1 {
		t.Fatalf("session count = %d, want 1", len(entries))
	}
	sessionDir := filepath.Join(sessions, entries[0].Name())
	transcript := filepath.Join(sessionDir, "transcript.md")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if !strings.Contains(string(data), "Hello world") {
		t.Fatalf("transcript %q missing the answer", data)
	}
	messages, err := os.ReadFile(filepath.Join(sessionDir, "messages.jsonl"))
	if err != nil {
		t.Fatalf("read message-layer transcript: %v", err)
	}
	if !strings.Contains(string(messages), "Hello world") {
		t.Fatalf("message-layer transcript %q missing the answer", messages)
	}
}

// TestRunBatchSubscribesForSecondSignalBeforeGracefulCancellation guards the
// batch lifecycle against losing a second interrupt during subscription handoff.
func TestRunBatchSubscribesForSecondSignalBeforeGracefulCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	origContext := batchSignalContext
	batchSignalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { batchSignalContext = origContext })

	armed := make(chan struct{})
	origSecond := batchSecondSignal
	batchSecondSignal = func() (<-chan os.Signal, func()) {
		close(armed)
		return make(chan os.Signal), func() {}
	}
	t.Cleanup(func() { batchSecondSignal = origSecond })

	result := make(chan error, 1)
	go func() {
		result <- Run(Options{
			DataDir:  filepath.Join(t.TempDir(), ".eitri"),
			LookPath: okLookPath,
			Prompt:   "wait",
			Provider: provider.NewScripted(func(ctx context.Context, _ provider.Request) (provider.Stream, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
		})
	}()

	select {
	case <-armed:
	case <-time.After(time.Second):
		t.Fatal("second-signal subscription was not installed before cancellation")
	}
	cancel()
	if err := <-result; !errors.Is(err, ErrBatchInterrupted) {
		t.Fatalf("Run() error = %v, want ErrBatchInterrupted", err)
	}
}

// TestRunBatchCompletionReleasesSecondSignalSubscription verifies normal completion
// releases the pre-armed second-interrupt subscription.
func TestRunBatchCompletionReleasesSecondSignalSubscription(t *testing.T) {
	orig := batchSecondSignal
	armed := 0
	released := 0
	batchSecondSignal = func() (<-chan os.Signal, func()) {
		armed++
		return make(chan os.Signal), func() { released++ }
	}
	t.Cleanup(func() { batchSecondSignal = orig })

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if armed != 1 || released != 1 {
		t.Fatalf("second-signal subscription armed/released = %d/%d, want 1/1", armed, released)
	}
}

// TestRunBatchClosesSession verifies the batch Run releases its session files
// before returning, so a completed run retains no open transcript or message log.
func TestRunBatchClosesSession(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".eitri")
	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if openSessionFile(t, dataDir) {
		t.Fatal("batch Run retained an open session file")
	}
}

func openSessionFile(t *testing.T, dataDir string) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read process file descriptors: %v", err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && strings.HasPrefix(target, dataDir+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
