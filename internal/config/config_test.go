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
	if !cfg.ThinkingEnabled {
		t.Fatal("ThinkingEnabled = false, want default true (on)")
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

// TestCopilotAndCustomOpenAITokensPersist verifies provider credentials for
// the non-default provider families round-trip through save/load: the Copilot
// device-flow tokens and the custom OpenAI endpoint+key are stored in config
// (T11). Copilot credentials must persist so a later batch run reuses the
// TUI-established session; custom OpenAI needs no device flow, key/setup only.
func TestCopilotAndCustomOpenAITokensPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.Provider = "github-copilot"
	cfg.Model = "gpt-4o"
	cfg.Copilot = CopilotConfig{AccessToken: "acc", RefreshToken: "ref", ExpiresAt: 123}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.Copilot.AccessToken != "acc" || got.Copilot.RefreshToken != "ref" || got.Copilot.ExpiresAt != 123 {
		t.Fatalf("Load() Copilot = %+v, want persisted tokens", got.Copilot)
	}

	// Lower the Provider to custom-openai and verify the endpoint+key persist.
	dir2 := t.TempDir()
	path2 := filepath.Join(dir2, "config.json")
	cfg2 := Default()
	cfg2.Provider = "custom-openai"
	cfg2.CustomOpenAI = OpenAIConfig{BaseURL: "https://my.endpoint/v1/chat/completions", Key: "k"}
	if err := Save(cfg2, path2); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	got2, err := Load(path2)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got2.CustomOpenAI.BaseURL != "https://my.endpoint/v1/chat/completions" || got2.CustomOpenAI.Key != "k" {
		t.Fatalf("Load() CustomOpenAI = %+v, want persisted endpoint+key", got2.CustomOpenAI)
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

// TestThinkingEnabledPersists verifies the thinking_enabled mode round-trips
// through save/load: an off value survives the round-trip so a session that
// disables reasoning is restored as non-thinking on reload (eitri.md §2.7).
func TestThinkingEnabledPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.ThinkingEnabled = false
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.ThinkingEnabled {
		t.Fatalf("Load() ThinkingEnabled = true, want persisted off (false)")
	}
}
