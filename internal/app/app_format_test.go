package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// TestRunBatchJSONEnvelope verifies --format json prints exactly one JSON object
// {answer, session, turns, stopped} and nothing else to stdout.
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
	// stdout carries nothing but the single envelope: exactly one JSON value.
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("stdout %q is not valid JSON", out.String())
	}
	if strings.Contains(out.String(), "Hello world") && !strings.Contains(trimmed, `"answer":"Hello world"`) {
		t.Fatalf("stdout %q leaks a bare answer outside the envelope", out.String())
	}
}

// TestRunBatchJSONVerboseStreamsThinkingToStderr verifies -v thinking goes to
// stderr under --format json, keeping stdout parseable as the single envelope.
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

// TestRunBatchTextVerboseStreamsThinkingToStderr verifies -v thinking moves to
// stderr under the default text format too, so stdout stays a clean answer.
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

// TestRunBatchTextFormatByteIdentical verifies --format text output is
// byte-identical to the pre-feature behavior: the streamed final answer only.
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

// TestValidateFormatRejectsUnknown verifies format validation dies before boot.
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

// TestRunBatchUnknownFormatFailsBeforeBoot verifies Run refuses an unknown
// format before any data directory is created.
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

// TestRunBatchTextNoticeForSkippedSkills verifies that a --format text batch
// run surfaces a lenient skill-discovery drop as an explicit notice on stderr
// (naming the skipped pack) while stdout stays a clean, byte-stable answer.
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

// TestRunBatchJSONNoSkipNoticeOnStdout verifies under --format json a skipped
// skill is surfaced only on stderr and never pollutes the stdout envelope.
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

// TestRunBatchJSONEnvelopeStoppedOnInterrupt verifies the first SIGINT/SIGTERM
// turns into a graceful stop: --format json still honors the envelope contract
// with stopped:true (so a script can tell an interrupted run from a failure),
// and Run returns nil so the session-temp cleanup defers run. The already-
// cancelled context stands in for the first signal without signalling the test
// process.
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
	if err != nil {
		t.Fatalf("Run(interrupted --format json) error = %v, want nil (graceful stop)", err)
	}
	var env struct {
		Answer  string `json:"answer"`
		Session string `json:"session"`
		Turns   int    `json:"turns"`
		Stopped bool   `json:"stopped"`
	}
	trimmed := strings.TrimSpace(out.String())
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON envelope: %v", out.String(), err)
	}
	if !env.Stopped {
		t.Fatal("interrupted envelope stopped = false, want true")
	}
	if env.Session == "" {
		t.Fatal("interrupted envelope missing session GUID")
	}
	if !json.Valid([]byte(out.String())) {
		t.Fatalf("stdout %q is not valid JSON on an interrupted run", out.String())
	}
}

// TestRunBatchTextOnInterruptEmitsNothing verifies a graceful stop under the
// default text format prints no partial answer to stdout and returns nil (the
// defers that clean up the session temp run before exit).
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
	if err != nil {
		t.Fatalf("Run(interrupted text) error = %v, want nil (graceful stop)", err)
	}
	if trimmed := strings.TrimSpace(out.String()); trimmed != "" {
		t.Fatalf("interrupted text run printed %q to stdout, want nothing", trimmed)
	}
}
