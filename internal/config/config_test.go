package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.MaxTurns != 250 {
		t.Fatalf("MaxTurns = %d, want default 250", cfg.MaxTurns)
	}
	if !cfg.ContextOverflowRecovery {
		t.Fatal("ContextOverflowRecovery = false, want default true")
	}
	if cfg.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want default \"low\"", cfg.ReasoningEffort)
	}
	if !cfg.ThinkingEnabled {
		t.Fatal("ThinkingEnabled = false, want default true (on)")
	}
	if !cfg.CoTCollapsedByDefault {
		t.Fatal("CoTCollapsedByDefault = false, want default true (collapsed by default)")
	}
	if !cfg.ToolResultsCollapsedByDefault {
		t.Fatal("ToolResultsCollapsedByDefault = false, want default true (collapsed by default)")
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
	if fi, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created at %s: %v", path, err)
	} else if fi.Size() == 0 {
		t.Fatalf("config file %s was created but is empty", path)
	}
}

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

func TestReasoningEffortDefaultAndPersist(t *testing.T) {
	t.Parallel()
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
	if err := Save(Config{MaxTurns: 7, ContextOverflowRecovery: true, ReasoningEffort: "max", Provider: "custom", Model: "m", ExtraWritablePaths: []string{"/tmp/x"}}, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.MaxTurns != 7 || !cfg.ContextOverflowRecovery || cfg.ReasoningEffort != "max" ||
		cfg.Provider != "custom" || cfg.Model != "m" {
		t.Fatalf("Load() = %+v, want persisted values {7 true max custom m}", cfg)
	}
	if len(cfg.ExtraWritablePaths) != 1 || cfg.ExtraWritablePaths[0] != "/tmp/x" {
		t.Fatalf("Load() ExtraWritablePaths = %v, want [/tmp/x]", cfg.ExtraWritablePaths)
	}
}

func TestCollapseDefaultsAbsentFromOldConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"opencode-go","thinking_enabled":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !got.CoTCollapsedByDefault {
		t.Fatal("Load() CoTCollapsedByDefault = false, want default true for absent field")
	}
	if !got.ToolResultsCollapsedByDefault {
		t.Fatal("Load() ToolResultsCollapsedByDefault = false, want default true for absent field")
	}
}

func TestCollapseDefaultsPersist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.CoTCollapsedByDefault = false
	cfg.ToolResultsCollapsedByDefault = false
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.CoTCollapsedByDefault {
		t.Fatalf("Load() CoTCollapsedByDefault = true, want persisted off (false)")
	}
	if got.ToolResultsCollapsedByDefault {
		t.Fatalf("Load() ToolResultsCollapsedByDefault = true, want persisted off (false)")
	}
}

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

func TestContextOverflowRecoveryAbsentFromOldConfigDefaultsOn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"opencode-go","compaction_fraction":0.5}`), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if !got.ContextOverflowRecovery {
		t.Fatal("Load() ContextOverflowRecovery = false, want default true for absent field")
	}
}

func TestSaveWritesContextOverflowRecoveryAndDropsCompactionFraction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	cfg.ContextOverflowRecovery = false
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"context_overflow_recovery": false`) {
		t.Fatalf("saved config %s missing context_overflow_recovery false", text)
	}
	if strings.Contains(text, "compaction_fraction") {
		t.Fatalf("saved config %s still contains compaction_fraction", text)
	}
}
