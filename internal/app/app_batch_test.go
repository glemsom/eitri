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

func TestRunBatchMissingGitPrintsSingleNonFatalNotice(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer

	// git is absent (soft dependency); the declared toolset is fully present.
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
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil — a missing soft dependency must not stop the run", err)
	}
	if !strings.Contains(out.String(), "Hello world") {
		t.Fatalf("batch output %q missing the final answer; run must complete normally", out.String())
	}
	n := strings.Count(errOut.String(), "eitri: ")
	if n != 1 {
		t.Fatalf("stderr carries %d boot notices (%q), want exactly one non-fatal notice", n, errOut.String())
	}
	if !strings.Contains(errOut.String(), "git") {
		t.Fatalf("boot notice %q does not name the missing soft dependency", errOut.String())
	}
}

func TestRunBatchGitPresentPrintsNoBootNotice(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want no git notice when git is present", errOut.String())
	}
}

func TestRunDeclaredRefusalStaysFatalWhenGitAlsoMissing(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer

	// bwrap (declared) and git (soft) are both absent: the declared refusal
	// must stay fatal — soft absence never suppresses a true missing-tool failure.
	missingBwrapAndGit := func(name string) (string, error) {
		if name == "git" || name == "bwrap" {
			return "", errors.New("executable not found: " + name)
		}
		return okLookPath(name)
	}

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: missingBwrapAndGit,
		Prompt:   "Say hello",
		Stdout:   &out,
		Stderr:   &errOut,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	})
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("Run() error = %v, want ErrMissingDependencies — declared refusals stay fatal with git absent", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when boot refuses on declared dependencies", out.String())
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
	transcript := filepath.Join(sessions, entries[0].Name(), "transcript.md")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if !strings.Contains(string(data), "Hello world") {
		t.Fatalf("transcript %q missing the answer", data)
	}
}
