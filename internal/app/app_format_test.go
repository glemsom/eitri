package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestRunBatchJSONEnvelope(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")

	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "json",
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch --format json) error = %v, want nil", err)
	}

	trimmed := strings.TrimSpace(out.String())
	var env struct {
		Answer  string `json:"answer"`
		Session string `json:"session"`
		Turns   int    `json:"turns"`
		Stopped bool   `json:"stopped"`
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", out.String(), err)
	}
	if env.Answer != "Hello world" {
		t.Fatalf("envelope answer = %q, want %q", env.Answer, "Hello world")
	}
	if env.Session == "" {
		t.Fatal("envelope session GUID is empty")
	}
	if env.Turns != 1 {
		t.Fatalf("envelope turns = %d, want 1", env.Turns)
	}
	if env.Stopped {
		t.Fatal("envelope stopped = true, want false for a normal batch run")
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("stdout %q is not valid JSON", out.String())
	}
	if strings.Contains(out.String(), "Hello world") && !strings.Contains(trimmed, `"answer":"Hello world"`) {
		t.Fatalf("stdout %q leaks a bare answer outside the envelope", out.String())
	}
}

func TestRunBatchJSONVerboseStreamsThinkingToStderr(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "json",
		Verbose:  true,
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch --format json -v) error = %v, want nil", err)
	}
	if !strings.Contains(errOut.String(), "think step by step") {
		t.Fatalf("thinking did not stream to stderr: %q", errOut.String())
	}
	if strings.Contains(out.String(), "think step by step") {
		t.Fatalf("thinking leaked to stdout under --format json: %q", out.String())
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("stdout %q is not a single JSON envelope", out.String())
	}
}

func TestRunBatchTextVerboseStreamsThinkingToStderr(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
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
		t.Fatalf("thinking did not stream to stderr: %q", errOut.String())
	}
	if strings.Contains(out.String(), "think step by step") {
		t.Fatalf("thinking leaked to stdout under text format: %q", out.String())
	}
	if strings.TrimSpace(out.String()) != "Hello world" {
		t.Fatalf("text stdout = %q, want only the answer", out.String())
	}
}

func TestRunBatchTextFormatByteIdentical(t *testing.T) {
	var out bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "text",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch --format text) error = %v, want nil", err)
	}
	if strings.TrimSpace(out.String()) != "Hello world" {
		t.Fatalf("text stdout = %q, want the final answer only", out.String())
	}
}

func TestValidateFormatRejectsUnknown(t *testing.T) {
	err := ValidateFormat("xml")
	if err == nil {
		t.Fatal("ValidateFormat(xml) = nil, want an error")
	}
	if !strings.Contains(err.Error(), `unknown --format "xml" (want: text, json)`) {
		t.Fatalf("ValidateFormat error = %q, want the unknown-format message", err)
	}
	if err := ValidateFormat("json"); err != nil {
		t.Fatalf("ValidateFormat(json) = %v, want nil", err)
	}
	if err := ValidateFormat("text"); err != nil {
		t.Fatalf("ValidateFormat(text) = %v, want nil", err)
	}
}

func TestRunBatchUnknownFormatFailsBeforeBoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".eitri")
	var out bytes.Buffer
	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "bogus",
		Stdout:   &out,
	})
	if err == nil {
		t.Fatal("Run(batch --format bogus) error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `unknown --format "bogus"`) {
		t.Fatalf("Run error = %q, want unknown-format message", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when format validation refuses boot", out.String())
	}
	if _, statErr := os.Stat(dataDir); statErr == nil {
		t.Fatalf("data dir %s was created before format validation", dataDir)
	}
}

func TestRunBatchTextNoticeForSkippedSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bad := filepath.Join(home, ".agents", "skills", "broken")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("# no frontmatter\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch text) error = %v, want nil", err)
	}
	if !strings.Contains(errOut.String(), "unparseable SKILL.md") || !strings.Contains(errOut.String(), "broken") {
		t.Fatalf("stderr missing skip notice, got: %q", errOut.String())
	}
	if strings.TrimSpace(out.String()) != "Hello world" {
		t.Fatalf("text stdout = %q, want clean answer \"Hello world\"", out.String())
	}
}

func TestRunBatchJSONNoSkipNoticeOnStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bad := filepath.Join(home, ".agents", "skills", "broken")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("# no frontmatter\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "json",
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch json) error = %v, want nil", err)
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("stdout %q is not valid JSON with a skipped skill present", out.String())
	}
}

// TestRunBatchJSONEnvelopeStoppedOnInterrupt stands in an already-cancelled
// context for the first signal instead of signalling the test process.
func TestRunBatchJSONEnvelopeStoppedOnInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	orig := batchSignalContext
	batchSignalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	defer func() { batchSignalContext = orig }()

	var out bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Format:   "json",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrBatchInterrupted) {
		t.Fatalf("Run(interrupted --format json) error = %v, want ErrBatchInterrupted", err)
	}
	if out.Len() != 0 {
		t.Fatalf("interrupted json run printed %q to stdout, want nothing", out.String())
	}
}

func TestRunBatchTextOnInterruptEmitsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	orig := batchSignalContext
	batchSignalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	defer func() { batchSignalContext = orig }()

	var out bytes.Buffer
	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrBatchInterrupted) {
		t.Fatalf("Run(interrupted text) error = %v, want ErrBatchInterrupted", err)
	}
	if trimmed := strings.TrimSpace(out.String()); trimmed != "" {
		t.Fatalf("interrupted text run printed %q to stdout, want nothing", trimmed)
	}
}
