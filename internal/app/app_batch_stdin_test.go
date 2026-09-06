package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// lastUserContent returns the content of the last user-role message the
// provider received, which in a batch run is the effective prompt (system-layer
// directives ride as their own messages and never follow it).
func lastUserContent(req provider.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// captureBatchPrompt runs one batch Run against a scripted provider and returns
// the effective prompt it received, so tests can assert exactly what reached
// the model without depending on the provider's reply.
func captureBatchPrompt(t *testing.T, opts Options) (string, error) {
	t.Helper()
	var got string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		got = lastUserContent(req)
		return provider.StreamFunc(
			provider.Chunk{Content: "ok"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
	opts.Provider = scripted
	if opts.Stdout == nil {
		opts.Stdout = &bytes.Buffer{}
	}
	err := Run(opts)
	return got, err
}

func TestRunBatchAppendsPipedStdinAsFencedContext(t *testing.T) {
	const prompt = "Review this diff"
	const input = "diff --git a/note.txt b/note.txt\n--- a/note.txt\n+++ b/note.txt\n@@ -1 +1 @@\n-old\n+new\n"

	got, err := captureBatchPrompt(t, Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   prompt,
		Stdin:    strings.NewReader(input),
	})
	if err != nil {
		t.Fatalf("Run(batch, piped stdin) error = %v, want nil", err)
	}
	want := prompt + "\n\nStdin input:\n```\n" + input + "```"
	if got != want {
		t.Fatalf("effective prompt = %q, want prompt + blank line + Stdin input: header + fenced block:\n%q", got, want)
	}
}

func TestRunBatchRefusesOversizedPipedStdin(t *testing.T) {
	const prompt = "Summarize this"
	// one byte over the 1 MiB cap: refusal must beat truncation, so the provider
	// must never see a partial prompt.
	oversized := strings.NewReader(strings.Repeat("x", 1<<20+1))

	called := false
	scripted := provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		called = true
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	})

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   prompt,
		Stdin:    oversized,
		Stdout:   &bytes.Buffer{},
		Provider: scripted,
	})
	if err == nil {
		t.Fatal("Run(batch, >1 MiB stdin) error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("refusal %q does not name the 1 MiB cap", err.Error())
	}
	if called {
		t.Fatal("provider was reached with oversized stdin; refusal must happen before any run")
	}
}

func TestRunBatchIgnoresEmptyPipedStdin(t *testing.T) {
	const prompt = "Say hello"

	got, err := captureBatchPrompt(t, Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   prompt,
		Stdin:    strings.NewReader(""),
	})
	if err != nil {
		t.Fatalf("Run(batch, empty stdin) error = %v, want nil", err)
	}
	if got != prompt {
		t.Fatalf("effective prompt = %q, want the prompt unchanged with no empty block", got)
	}
}

func TestRunBatchIgnoresDevNullStdin(t *testing.T) {
	// `eitri -b "<prompt>" < /dev/null` must be indistinguishable from no stdin:
	// the prompt stays byte-identical and no empty Stdin block is appended.
	const prompt = "Say hello"

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer devnull.Close()

	got, err := captureBatchPrompt(t, Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Prompt:   prompt,
		Stdin:    devnull,
	})
	if err != nil {
		t.Fatalf("Run(batch, </dev/null) error = %v, want nil", err)
	}
	if got != prompt {
		t.Fatalf("effective prompt = %q, want the prompt unchanged under </dev/null", got)
	}
}

func TestRunInteractiveLaunchNotRefusedByDevNullStdin(t *testing.T) {
	// A detached/redirected launch (`eitri < /dev/null`) is a character device,
	// not a pipe, so the TUI must still boot: only piped data that would be
	// silently drained triggers the refusal.
	stubTUIEnv(t, interactiveEnv)
	launched := recordingTUI(t)
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = orig })

	if err := Run(Options{DataDir: filepath.Join(t.TempDir(), ".eitri"), LookPath: okLookPath}); err != nil {
		t.Fatalf("Run(TUI, stdin=/dev/null) error = %v, want nil", err)
	}
	if !*launched {
		t.Fatal("TUI did not launch with /dev/null stdin; a character device is not piped input")
	}
}

func TestRunRefusesInteractiveLaunchWhenStdinPiped(t *testing.T) {
	stubTUIEnv(t, interactiveEnv)
	launched := recordingTUI(t)

	err := Run(Options{
		DataDir:  filepath.Join(t.TempDir(), ".eitri"),
		LookPath: okLookPath,
		Stdin:    strings.NewReader("git diff output"),
	})
	if !errors.Is(err, ErrStdinWithoutBatch) {
		t.Fatalf("Run(stdin piped, no -b) error = %v, want ErrStdinWithoutBatch", err)
	}
	if !strings.Contains(err.Error(), `eitri -b "<prompt>"`) {
		t.Fatalf("refusal %q does not point at `eitri -b \"<prompt>\"`", err.Error())
	}
	if *launched {
		t.Fatal("TUI launched with piped stdin; the piped data would be silently drained")
	}
}
