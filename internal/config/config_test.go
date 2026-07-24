package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Verify file does not exist before load
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("test config should not exist yet")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil for missing file", err)
	}

	// Default config values
	if cfg.Provider != "opencode_go" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "opencode_go")
	}
	if cfg.BaseURL != "https://opencode.ai/zen/go" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://opencode.ai/zen/go")
	}
	if cfg.Model != "" {
		t.Errorf("Model = %q, want empty string", cfg.Model)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey should be empty, got %q", cfg.APIKey)
	}
	if cfg.SessionTimeout != 30*60_000_000_000 {
		t.Errorf("SessionTimeout = %d, want 30m in ns", cfg.SessionTimeout)
	}
	if cfg.CommandTimeout != 60_000_000_000 {
		t.Errorf("CommandTimeout = %d, want 60s in ns", cfg.CommandTimeout)
	}
	if cfg.MaxTurns != 75 {
		t.Errorf("MaxTurns = %d, want 75", cfg.MaxTurns)
	}
	if cfg.ContextWindowTokens != 256000 {
		t.Errorf("ContextWindowTokens = %d, want 256000", cfg.ContextWindowTokens)
	}

	// File must NOT have been created
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatal("Load() created config file — must not create file when missing")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{
		"provider": "custom_openai",
		"api_key": "sk-test123",
		"base_url": "https://custom.example.com",
		"model": "gpt-4",
		"session_timeout": 60000000000,
		"command_timeout": 120000000000,
		"max_turns": 10,
		"context_window_tokens": 128000
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.Provider != "custom_openai" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "custom_openai")
	}
	if cfg.APIKey != "sk-test123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test123")
	}
	if cfg.BaseURL != "https://custom.example.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://custom.example.com")
	}
	if cfg.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", cfg.Model, "gpt-4")
	}
	if cfg.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", cfg.MaxTurns)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := config.Defaults()
	cfg.Provider = "custom_openai"
	cfg.APIKey = "sk-saved"
	cfg.Model = "claude-3"

	if err := config.Save(cfgPath, &cfg); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	// Verify file exists with correct permissions
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("config file permissions = %o, want 0600", info.Mode().Perm())
	}

	// Load back and verify
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() after save = %v", err)
	}
	if loaded.Provider != cfg.Provider {
		t.Errorf("Provider = %q, want %q", loaded.Provider, cfg.Provider)
	}
	if loaded.Model != cfg.Model {
		t.Errorf("Model = %q, want %q", loaded.Model, cfg.Model)
	}
}

func TestDefaults_ReturnsCopy(t *testing.T) {
	a := config.Defaults()
	b := config.Defaults()

	a.Provider = "custom_openai"
	if b.Provider == "custom_openai" {
		t.Error("Defaults() returned shared state — must return independent copy")
	}
}

func TestValidate_ValidOpenCodeWithAPIKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test-key"
	if err := config.Validate(&cfg); err != nil {
		t.Errorf("Validate(valid opencode_go) = %v, want nil", err)
	}
}

func TestValidate_InvalidProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider = "invalid_provider"
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(invalid provider) = nil, want error")
	}
}

func TestValidate_MissingAPIKeyForOpenCode(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = ""
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(empty api_key for opencode_go) = nil, want error")
	}
}

func TestValidate_CustomOpenAIAPIKeyOptional(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider = "custom_openai"
	cfg.APIKey = ""
	cfg.BaseURL = "https://custom.example.com"
	if err := config.Validate(&cfg); err != nil {
		t.Errorf("Validate(custom_openai missing key) = %v, want nil (key is optional)", err)
	}
}

func TestValidate_GitHubCopilotAcceptsProviderAuthState(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider = "github_copilot"
	cfg.APIKey = ""
	cfg.BaseURL = "https://api.githubcopilot.com"
	var err error
	cfg.ProviderAuth, err = provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{AccessToken: "gho-provider-state"})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}
	if err := config.Validate(&cfg); err != nil {
		t.Fatalf("Validate(github_copilot provider_auth) = %v, want nil", err)
	}
}

func TestValidate_MissingTokenForGitHubCopilot(t *testing.T) {
	cfg := config.Defaults()
	cfg.Provider = "github_copilot"
	cfg.APIKey = ""
	cfg.BaseURL = "https://api.githubcopilot.com"
	err := config.Validate(&cfg)
	if err == nil {
		t.Fatal("Validate(github_copilot missing token) = nil, want error")
	}
	if !strings.Contains(err.Error(), "token is required") {
		t.Errorf("error = %q, want missing token", err.Error())
	}
}

func TestValidate_SessionTimeoutMin(t *testing.T) {
	cfg := config.Defaults()
	cfg.SessionTimeout = 500_000_000 // 0.5s < 1 minute
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(session_timeout < 1min) = nil, want error")
	}
}

func TestValidate_CommandTimeoutMin(t *testing.T) {
	cfg := config.Defaults()
	cfg.CommandTimeout = 500_000_000 // 0.5s < 1 second
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(command_timeout < 1s) = nil, want error")
	}
}

func TestValidate_MaxTurnsMin(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxTurns = 0
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(max_turns < 1) = nil, want error")
	}
}

func TestValidate_ContextWindowMin(t *testing.T) {
	cfg := config.Defaults()
	cfg.ContextWindowTokens = 512
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(context_window_tokens < 1024) = nil, want error")
	}
}

func TestValidate_InvalidBaseURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.BaseURL = "not-a-url"
	if err := config.Validate(&cfg); err == nil {
		t.Error("Validate(bad base_url) = nil, want error")
	}
}

func TestValidateSelectedModel_RequiresSelection(t *testing.T) {
	cfg := config.Defaults()
	cfg.Model = ""

	err := config.ValidateSelectedModel(&cfg, []string{"gpt-4"})
	if err == nil {
		t.Fatal("ValidateSelectedModel(empty) = nil, want error")
	}
	if !strings.Contains(err.Error(), "model is required") {
		t.Errorf("error = %q, want missing model message", err.Error())
	}
}

func TestValidateSelectedModel_RequiresDiscoveredModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Model = "stale-model"

	err := config.ValidateSelectedModel(&cfg, []string{"gpt-4"})
	if err == nil {
		t.Fatal("ValidateSelectedModel(stale) = nil, want error")
	}
	if !strings.Contains(err.Error(), "stale-model") {
		t.Errorf("error = %q, want selected model name", err.Error())
	}
}

func TestValidateSelectedModel_AcceptsDiscoveredModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Model = "gpt-4"

	if err := config.ValidateSelectedModel(&cfg, []string{"gpt-4", "gpt-4.1"}); err != nil {
		t.Errorf("ValidateSelectedModel(valid) = %v, want nil", err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"sk-abc", "sk-abc"}, // too short to mask
		{"sk-abcdefghijklm", "sk-ab...klm"},
		{"sk-abcdefghijklmnop", "sk-ab...nop"},
	}
	for _, tt := range tests {
		got := config.MaskAPIKey(tt.input)
		if got != tt.want {
			t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMerge_OverridesProvider(t *testing.T) {
	cfg := config.Defaults()
	patch := map[string]any{
		"provider": "custom_openai",
		"base_url": "https://other.example.com",
	}
	result := config.Merge(&cfg, patch)

	if result.Provider != "custom_openai" {
		t.Errorf("Provider = %q, want %q", result.Provider, "custom_openai")
	}
	if result.BaseURL != "https://other.example.com" {
		t.Errorf("BaseURL = %q, want %q", result.BaseURL, "https://other.example.com")
	}
	// Unset fields should keep defaults
	if result.APIKey != cfg.APIKey {
		t.Errorf("APIKey changed unexpectedly: %q", result.APIKey)
	}
}

func TestMerge_IgnoresUnknownFields(t *testing.T) {
	cfg := config.Defaults()
	patch := map[string]any{
		"nonexistent": "value",
	}
	result := config.Merge(&cfg, patch)
	if result.Provider != cfg.Provider {
		t.Errorf("Provider changed unexpectedly: %q", result.Provider)
	}
}

func TestMerge_ClearAPIKey(t *testing.T) {
	for _, clearValue := range []any{"true", true} {
		t.Run(fmt.Sprintf("%T", clearValue), func(t *testing.T) {
			cfg := config.Defaults()
			cfg.APIKey = "sk-secret-key-to-clear"
			patch := map[string]any{
				"clear_api_key": clearValue,
			}
			result := config.Merge(&cfg, patch)
			if result.APIKey != "" {
				t.Errorf("APIKey = %q, want empty after clear", result.APIKey)
			}
			// Other fields should remain unchanged
			if result.Provider != cfg.Provider {
				t.Errorf("Provider changed unexpectedly: %q", result.Provider)
			}
		})
	}
}

func TestMerge_PreservesAPIKeyWhenEmptyWithoutClear(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-existing"

	result := config.Merge(&cfg, map[string]any{"api_key": ""})

	if result.APIKey != "sk-existing" {
		t.Errorf("APIKey = %q, want existing key preserved", result.APIKey)
	}
}

func TestLoad_SetsAllowedReadPaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{
		"provider": "opencode_go",
		"api_key": "sk-test",
		"allowed_read_paths": ["/home/user/projects", "/tmp/shared"]
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if len(cfg.AllowedReadPaths) != 2 {
		t.Fatalf("AllowedReadPaths = %v, want 2 entries", cfg.AllowedReadPaths)
	}
	if cfg.AllowedReadPaths[0] != "/home/user/projects" {
		t.Errorf("AllowedReadPaths[0] = %q, want %q", cfg.AllowedReadPaths[0], "/home/user/projects")
	}
	if cfg.AllowedReadPaths[1] != "/tmp/shared" {
		t.Errorf("AllowedReadPaths[1] = %q, want %q", cfg.AllowedReadPaths[1], "/tmp/shared")
	}
}

func TestSave_RoundTripsAllowedReadPaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := config.Defaults()
	cfg.APIKey = "sk-save-test"
	cfg.AllowedReadPaths = []string{"/home/user/projects", "/tmp/shared"}

	if err := config.Save(cfgPath, &cfg); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() after save = %v", err)
	}

	if len(loaded.AllowedReadPaths) != 2 {
		t.Fatalf("AllowedReadPaths = %v, want 2 entries", loaded.AllowedReadPaths)
	}
	if loaded.AllowedReadPaths[0] != "/home/user/projects" {
		t.Errorf("AllowedReadPaths[0] = %q, want %q", loaded.AllowedReadPaths[0], "/home/user/projects")
	}
	if loaded.AllowedReadPaths[1] != "/tmp/shared" {
		t.Errorf("AllowedReadPaths[1] = %q, want %q", loaded.AllowedReadPaths[1], "/tmp/shared")
	}
}

func TestLoad_AllowedReadPathsNilWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{
		"provider": "opencode_go",
		"api_key": "sk-test"
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.AllowedReadPaths != nil {
		t.Errorf("AllowedReadPaths = %v, want nil when field omitted", cfg.AllowedReadPaths)
	}
}

func TestMerge_AllowedReadPaths(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	patch := map[string]any{
		"allowed_read_paths": []any{"/home/user/projects", "/tmp/shared"},
	}
	result := config.Merge(&cfg, patch)

	if result.AllowedReadPaths == nil {
		t.Fatal("AllowedReadPaths is nil after merge")
	}
	if len(result.AllowedReadPaths) != 2 {
		t.Fatalf("AllowedReadPaths = %v, want 2 entries", result.AllowedReadPaths)
	}
	if result.AllowedReadPaths[0] != "/home/user/projects" {
		t.Errorf("AllowedReadPaths[0] = %q, want %q", result.AllowedReadPaths[0], "/home/user/projects")
	}
	if result.AllowedReadPaths[1] != "/tmp/shared" {
		t.Errorf("AllowedReadPaths[1] = %q, want %q", result.AllowedReadPaths[1], "/tmp/shared")
	}
}

func TestMerge_AllowedReadPathsIgnoresNonArray(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	patch := map[string]any{
		"allowed_read_paths": "not-an-array",
	}
	result := config.Merge(&cfg, patch)

	if result.AllowedReadPaths != nil {
		t.Errorf("AllowedReadPaths = %v, want nil for non-array patch", result.AllowedReadPaths)
	}
}

func TestMerge_ProviderSwitchClearsModelAndResetsDefaultBaseURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Model = "opencode-model"

	result := config.Merge(&cfg, map[string]any{"provider": "github_copilot"})

	if result.Model != "" {
		t.Errorf("Model = %q, want cleared on provider switch", result.Model)
	}
	if result.BaseURL != "https://api.githubcopilot.com" {
		t.Errorf("BaseURL = %q, want GitHub Copilot default", result.BaseURL)
	}
}

func TestMerge_ProviderSwitchPreservesExplicitSubmittedModelAndResetsStaleDefaultBaseURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Model = "opencode-model"

	result := config.Merge(&cfg, map[string]any{
		"provider": "github_copilot",
		"base_url": cfg.BaseURL,
		"model":    "gpt-4.1",
	})

	if result.Model != "gpt-4.1" {
		t.Errorf("Model = %q, want explicit submitted model preserved", result.Model)
	}
	if result.BaseURL != "https://api.githubcopilot.com" {
		t.Errorf("BaseURL = %q, want GitHub Copilot default", result.BaseURL)
	}
}

func TestMerge_ProviderSwitchPreservesCustomBaseURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.BaseURL = "https://custom-gateway.example.com/v1"
	cfg.Model = "opencode-model"

	result := config.Merge(&cfg, map[string]any{"provider": "github_copilot"})

	if result.Model != "" {
		t.Errorf("Model = %q, want cleared on provider switch", result.Model)
	}
	if result.BaseURL != cfg.BaseURL {
		t.Errorf("BaseURL = %q, want custom base URL preserved", result.BaseURL)
	}
}

func TestMerge_OverridesSystemPrompt(t *testing.T) {
	cfg := config.Defaults()
	cfg.SystemPrompt = "old prompt"

	result := config.Merge(&cfg, map[string]any{"system_prompt": "new prompt"})

	if result.SystemPrompt != "new prompt" {
		t.Errorf("SystemPrompt = %q, want %q", result.SystemPrompt, "new prompt")
	}
}
