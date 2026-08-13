package app

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

// captureThinking records the thinking-control head of every provider request,
// so a test can assert the run seam reads config.ThinkingEnabled instead of
// hardcoding thinking on (eitri.md §2.7 / #55).
type captureThinking struct {
	seen []bool
}

// TestRunReadsThinkingEnabledConfigFalse drives batch mode with a config that
// has thinking_enabled:false and asserts the provider request carries
// ThinkingEnabled off and no reasoning_effort — the non-thinking run path.
func TestRunReadsThinkingEnabledConfigFalse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.ThinkingEnabled = false
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	var capThinking captureThinking
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		capThinking.seen = append(capThinking.seen, req.ThinkingEnabled)
		if capThinking.seen[0] {
			t.Errorf("provider request ThinkingEnabled = true, want false for thinking_enabled:false config")
		}
		if req.ReasoningEffort != "" {
			t.Errorf("provider request ReasoningEffort = %q, want empty when thinking is off", req.ReasoningEffort)
		}
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	})

	err := Run(Options{
		DataDir:    filepath.Join(dir, ".eitri"),
		ConfigPath: cfgPath,
		LookPath:   okLookPath,
		Prompt:     "go",
		Stdout:     &bytes.Buffer{},
		Provider:   scripted,
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if len(capThinking.seen) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(capThinking.seen))
	}
}

// TestRunReadsThinkingEnabledConfigDefaultTrue verifies the default config
// keeps thinking on: batch mode with an injected provider yields a request
// with ThinkingEnabled true (unchanged default behaviour).
func TestRunReadsThinkingEnabledConfigDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Default() // thinking_enabled defaults to true
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	var capThinking captureThinking
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		capThinking.seen = append(capThinking.seen, req.ThinkingEnabled)
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	})

	err := Run(Options{
		DataDir:    filepath.Join(dir, ".eitri"),
		ConfigPath: cfgPath,
		LookPath:   okLookPath,
		Prompt:     "go",
		Stdout:     &bytes.Buffer{},
		Provider:   scripted,
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if len(capThinking.seen) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(capThinking.seen))
	}
	if !capThinking.seen[0] {
		t.Error("provider request ThinkingEnabled = false, want true for default config")
	}
}
