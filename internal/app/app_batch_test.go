package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestRunBatchYoloStartsWithoutBwrapOnPath(t *testing.T) {
	missingBwrap := func(name string) (string, error) {
		if name == "bwrap" {
			return "", errors.New("executable not found: bwrap")
		}
		return okLookPath(name)
	}

	var out bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: missingBwrap,
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
	missingBwrap := func(name string) (string, error) {
		if name == "bwrap" {
			return "", errors.New("executable not found: bwrap")
		}
		return okLookPath(name)
	}

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: missingBwrap,
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
