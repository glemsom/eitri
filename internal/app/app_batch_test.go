package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// TestRunBatchReturnsAnswer drives batch mode end-to-end: an injected fake
// provider backs the engine, and Run prints the final assistant answer to the
// configured output writer.
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

// TestRunBatchSuppressesThinkingByDefault verifies reasoning is not printed to
// stdout unless Verbose is set (docs/spec.md §6).
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

// TestRunBatchVerboseShowsThinking verifies -v surfaces reasoning.
func TestRunBatchVerboseShowsThinking(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Verbose:  true,
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch -v) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "think step by step") {
		t.Fatalf("verbose output %q missing reasoning", out.String())
	}
}

// TestRunBatchWritesTranscript verifies the batch answer lands on the session
// transcript via the T1b sink.
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
	transcript := filepath.Join(sessions, entries[0].Name(), "transcript.md")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if !strings.Contains(string(data), "Hello world") {
		t.Fatalf("transcript %q missing the answer", data)
	}
}
