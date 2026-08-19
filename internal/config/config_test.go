package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.MaxTurns != 250 {
		t.Fatalf("MaxTurns = %d, want default 250", cfg.MaxTurns)
	}
	if cfg.CompactionFraction != 0.8 {
		t.Fatalf("CompactionFraction = %v, want default 0.8", cfg.CompactionFraction)
	}
	if cfg.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want default \"low\"", cfg.ReasoningEffort)
	}
	if !cfg.ThinkingEnabled {
		t.Fatal("ThinkingEnabled = false, want default true (on)")
	}
	if cfg.Provider != "opencode-go" {
		t.Fatalf("Provider = %q, want default opencode-go primary", cfg.Provider)
	}
	if cfg.Model != "deepseek-v4-flash" {
		if cfg.Theme != "dark" {
			t.Fatalf("Theme = %q, want default \"dark\"", cfg.Theme)
		}
		t.Fatalf("Model = %q, want default deepseek-v4-flash", cfg.Model)
	}
}

func TestLoadCreatesConfigWithDefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
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
// device-flow tokens and the custom OpenAI endpoint+key are stored in config.
// Copilot credentials must persist so a later batch run reuses the
// TUI-established session; custom OpenAI needs no device flow, key/setup only.
func TestCopilotAndCustomOpenAITokensPersist(t *testing.T) {
	t.Parallel()
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

// TestReasoningEffortDefaultAndPersist verifies the acceptance criteria for
// the reasoning-effort default change: a config written before the
// change that stored "high" still loads "high", while an absent
// reasoning_effort field loads the new "low" default.
func TestReasoningEffortDefaultAndPersist(t *testing.T) {
	t.Parallel()
	// A present stored value is authoritative and survives the round-trip.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Save(Config{ReasoningEffort: "high"}, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("Load() ReasoningEffort = %q, want persisted \"high\"", got.ReasoningEffort)
	}
}

// A config file that never saved a reasoning_effort (older file) must load
// with the new "low" default rather than the zero value.
func TestReasoningEffortAbsentDefaultsToLow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{\"provider\": \"opencode-go\", \"thinking_enabled\": true}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.ReasoningEffort != "low" {
		t.Fatalf("Load() ReasoningEffort = %q, want default \"low\" for absent field", got.ReasoningEffort)
	}
}

// TestThemeAbsentDefaultsToDark verifies the theme acceptance criteria: a config
// file written before the theme feature (no `theme` key) loads
// with the "dark" default rather than the empty zero value.
func TestThemeAbsentDefaultsToDark(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{\"provider\": \"opencode-go\", \"reasoning_effort\": \"high\", \"thinking_enabled\": true}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v, want nil", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.Theme != "dark" {
		t.Fatalf("Load() Theme = %q, want default \"dark\" for absent field", got.Theme)
	}
}

// TestThemePersists verifies a chosen theme round-trips through save/load so a
// user's render-theme pick survives a reload.
func TestThemePersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.Theme = "tokyo-night"
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.Theme != "tokyo-night" {
		t.Fatalf("Load() Theme = %q, want persisted \"tokyo-night\"", got.Theme)
	}
}

func TestLoadReadsPersistedConfig(t *testing.T) {
	t.Parallel()
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
// disables reasoning is restored as non-thinking on reload.
func TestThinkingEnabledPersists(t *testing.T) {
	t.Parallel()
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

// TestRailWidthPersists verifies the rail_width round-trips through save/load:
// a non-zero width survives reload, while zero (absent in old configs) stays at
// zero so the TUI can fall back to the compiled default.
func TestRailWidthPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.RailWidth = 45
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.RailWidth != 45 {
		t.Fatalf("Load() RailWidth = %d, want 45", got.RailWidth)
	}
}

// TestRailWidthZeroRoundTrips verifies that an explicitly stored zero value
// round-trips faithfully (omitempty would omit it, but JSON zero is valid).
func TestRailWidthZeroRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := Save(Config{RailWidth: 0}, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.RailWidth != 0 {
		t.Fatalf("Load() RailWidth = %d, want 0", got.RailWidth)
	}
}

// TestRailWidthAbsentFromOldConfig verifies a config file written before the
// rail_width field loads without error and leaves RailWidth at zero, letting
// the TUI fall back to DefaultRailWidth.
func TestRailWidthAbsentFromOldConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"opencode-go"}`), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.RailWidth != 0 {
		t.Fatalf("Load() RailWidth = %d, want 0 for absent field", got.RailWidth)
	}
}
