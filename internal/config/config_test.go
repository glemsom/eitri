package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Default()
	if cfg.MaxTurns != 250 {
		t.Fatalf("MaxTurns = %d, want default 250", cfg.MaxTurns)
	}
	if cfg.CompactionFraction != 0.8 {
		t.Fatalf("CompactionFraction = %v, want default 0.8", cfg.CompactionFraction)
	}
	if cfg.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want default \"high\"", cfg.ReasoningEffort)
	}
	if cfg.Provider != "opencode-go" {
		t.Fatalf("Provider = %q, want default opencode-go primary", cfg.Provider)
	}
	if cfg.Model != "deepseek-v4-flash" {
		t.Fatalf("Model = %q, want default deepseek-v4-flash", cfg.Model)
	}
}

func TestLoadCreatesConfigWithDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got := cfg.MaxTurns; got != 250 {
		t.Fatalf("loaded absent config MaxTurns = %d, want default 250", got)
	}
	// The file must now exist on disk with the defaults persisted.
	if fi, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created at %s: %v", path, err)
	} else if fi.Size() == 0 {
		t.Fatalf("config file %s was created but is empty", path)
	}
}

func TestLoadReadsPersistedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Save(Config{MaxTurns: 7, CompactionFraction: 0.5, ReasoningEffort: "max", Provider: "custom", Model: "m", ExtraWritablePaths: []string{"/tmp/x"}}, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.MaxTurns != 7 || cfg.CompactionFraction != 0.5 || cfg.ReasoningEffort != "max" ||
		cfg.Provider != "custom" || cfg.Model != "m" {
		t.Fatalf("Load() = %+v, want persisted values {7 0.5 max custom m}", cfg)
	}
	if len(cfg.ExtraWritablePaths) != 1 || cfg.ExtraWritablePaths[0] != "/tmp/x" {
		t.Fatalf("Load() ExtraWritablePaths = %v, want [/tmp/x]", cfg.ExtraWritablePaths)
	}
}
