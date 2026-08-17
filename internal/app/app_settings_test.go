package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
)

// recordedProvider is a scripted provider that records every outgoing request
// so a test can assert which configured values the engine actually used.
func recordedProvider(t *testing.T, reqs *[]provider.Request, finish bool) provider.Provider {
	t.Helper()
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		*reqs = append(*reqs, req)
		if finish {
			return provider.StreamFunc(
				provider.Chunk{Content: "ans", FinishReason: "stop", Done: true},
			), nil
		}
		// Never finish: a real cap (not provider behavior) must bound the loop.
		return provider.StreamFunc(
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true},
		), nil
	})
}

// TestRunSettingsRespectsPersistedConfig verifies settings saved to config
// round-trip and drive a subsequent run: a Custom model + max-effort persisted
// in config.json are used by the engine on the next batch run.
func TestRunSettingsRespectsPersistedConfig(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	cfgPath := filepath.Join(dataDir, "config.json")
	cfg := config.Config{
		Provider:        "opencode-go",
		Model:           "grok-2",
		ReasoningEffort: "max",
		ThinkingEnabled: true, // thinking stays on; effort is dialed to max
		MaxTurns:        0,    // uncapped: the provider finishes normally
	}
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}

	var reqs []provider.Request
	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "do it",
		Stdout:   &bytes.Buffer{},
		Provider: recordedProvider(t, &reqs, true),
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(reqs) == 0 {
		t.Fatal("provider never called; engine did not run")
	}
	if reqs[0].Model != "grok-2" {
		t.Fatalf("engine Model = %q, want persisted grok-2", reqs[0].Model)
	}
	if reqs[0].ReasoningEffort != "max" {
		t.Fatalf("engine ReasoningEffort = %q, want persisted max", reqs[0].ReasoningEffort)
	}
}

// TestRunBatchHonorsMaxTurnsFromConfig verifies the persisted max_turns cap is
// honored by the engine in batch: with a low cap and no continuation hook (the
// batch default), the run auto-denies with ErrMaxTurns instead of looping
// forever.
func TestRunBatchHonorsMaxTurnsFromConfig(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	cfgPath := filepath.Join(dataDir, "config.json")
	cfg := config.Config{Provider: "opencode-go", Model: "deepseek-v4-flash", MaxTurns: 2}
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}

	var reqs []provider.Request
	err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "loop",
		Stdout:   &bytes.Buffer{},
		Provider: recordedProvider(t, &reqs, false), // never finishes
	})
	if !errors.Is(err, engine.ErrMaxTurns) {
		t.Fatalf("Run() error = %v, want engine.ErrMaxTurns (batch auto-deny at cap)", err)
	}
}

// test; kept minimal to ensure strings stays imported for compile.
