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

	// ActivePersona should default to "generic" when not set
	if cfg.ActivePersona != "generic" {
		t.Errorf("ActivePersona = %q, want %q", cfg.ActivePersona, "generic")
	}
}

func TestLoad_ActivePersonaPreservedWhenSet(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{
		"provider": "custom_openai",
		"api_key": "sk-test123",
		"base_url": "https://custom.example.com",
		"active_persona": "my-custom-agent"
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.ActivePersona != "my-custom-agent" {
		t.Errorf("ActivePersona = %q, want %q", cfg.ActivePersona, "my-custom-agent")
	}
}

func TestLoad_ActivePersonaDefaultsToGenericWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{
		"provider": "custom_openai",
		"api_key": "sk-test123",
		"active_persona": ""
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	if cfg.ActivePersona != "generic" {
		t.Errorf("ActivePersona = %q, want %q", cfg.ActivePersona, "generic")
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

func TestSave_RoundTripsUserEmail(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := config.Defaults()
	cfg.APIKey = "sk-email-test"
	cfg.UserEmail = "user@example.com"

	if err := config.Save(cfgPath, &cfg); err != nil {
		t.Fatalf("Save() = %v, want nil", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() after save = %v", err)
	}

	if loaded.UserEmail != "user@example.com" {
		t.Errorf("UserEmail = %q, want %q", loaded.UserEmail, "user@example.com")
	}
}

func TestLoad_UserEmailEmptyWhenMissing(t *testing.T) {
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

	if cfg.UserEmail != "" {
		t.Errorf("UserEmail = %q, want empty when field omitted", cfg.UserEmail)
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

func TestMerge_OverridesUserEmail(t *testing.T) {
	cfg := config.Defaults()

	result := config.Merge(&cfg, map[string]any{"user_email": "user@example.com"})

	if result.UserEmail != "user@example.com" {
		t.Errorf("UserEmail = %q, want %q", result.UserEmail, "user@example.com")
	}
}

func TestMerge_ClearsUserEmail(t *testing.T) {
	cfg := config.Defaults()
	cfg.UserEmail = "user@example.com"

	result := config.Merge(&cfg, map[string]any{"user_email": ""})

	if result.UserEmail != "" {
		t.Errorf("UserEmail = %q, want empty", result.UserEmail)
	}
}

// --- Debug field tests ---

func TestLoad_PromotesDebugPromptEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Config file without debug_prompt
	content := `{"provider": "opencode_go", "api_key": "sk-test"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EITRI_DEBUG_PROMPT", "1")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.DebugPrompt {
		t.Error("DebugPrompt = false, want true (promoted from EITRI_DEBUG_PROMPT)")
	}
}

func TestLoad_PromotesDebugRequestEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{"provider": "opencode_go", "api_key": "sk-test"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EITRI_DEBUG_REQUEST", "1")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.DebugRequest {
		t.Error("DebugRequest = false, want true (promoted from EITRI_DEBUG_REQUEST)")
	}
}

func TestLoad_PromotesDebugLLMDirEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{"provider": "opencode_go", "api_key": "sk-test"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EITRI_DEBUG_LLM_DIR", "/tmp/llm-debug")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.DebugLLMDir != "/tmp/llm-debug" {
		t.Errorf("DebugLLMDir = %q, want %q", cfg.DebugLLMDir, "/tmp/llm-debug")
	}
}

func TestLoad_DoesNotOverrideSetDebugFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Config has debug_prompt explicitly set to true and debug_llm_dir set
	content := `{"provider": "opencode_go", "api_key": "sk-test", "debug_prompt": true, "debug_llm_dir": "/custom/path"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EITRI_DEBUG_PROMPT", "1")
	t.Setenv("EITRI_DEBUG_LLM_DIR", "/env/path")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.DebugPrompt {
		t.Error("DebugPrompt = false, want true from config file")
	}
	if cfg.DebugLLMDir != "/custom/path" {
		t.Errorf("DebugLLMDir = %q, want %q (config file value should take precedence)", cfg.DebugLLMDir, "/custom/path")
	}
}

func TestLoad_EnvVarOverridesExplicitFalseDebugPrompt(t *testing.T) {
	// When debug_prompt is explicitly false in config but env var is set,
	// the env var wins because false is the zero value for bool.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	content := `{"provider": "opencode_go", "api_key": "sk-test", "debug_prompt": false}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EITRI_DEBUG_PROMPT", "1")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.DebugPrompt {
		t.Error("DebugPrompt = false, want true (env var overrides zero-value false)")
	}
}

func TestLoad_PromotesEnvVarsWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	t.Setenv("EITRI_DEBUG_PROMPT", "1")
	t.Setenv("EITRI_DEBUG_LLM_DIR", "/tmp/llm-debug")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if !cfg.DebugPrompt {
		t.Error("DebugPrompt = false, want true (promoted from env when file missing)")
	}
	if cfg.DebugLLMDir != "/tmp/llm-debug" {
		t.Errorf("DebugLLMDir = %q, want %q", cfg.DebugLLMDir, "/tmp/llm-debug")
	}
}

func TestValidate_RejectsRelativeDebugLLMDir(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test-key"
	cfg.DebugLLMDir = "relative/path"

	err := config.Validate(&cfg)
	if err == nil {
		t.Fatal("Validate(relative debug_llm_dir) = nil, want error")
	}
	if !strings.Contains(err.Error(), "absolute directory path") {
		t.Errorf("error = %q, want message about absolute directory path", err.Error())
	}
}

func TestValidate_AcceptsAbsoluteDebugLLMDir(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"
	cfg.DebugLLMDir = "/tmp/llm-debug"

	if err := config.Validate(&cfg); err != nil {
		t.Errorf("Validate(absolute debug_llm_dir) = %v, want nil", err)
	}
}

func TestValidate_AcceptsEmptyDebugLLMDir(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"
	cfg.DebugLLMDir = ""

	if err := config.Validate(&cfg); err != nil {
		t.Errorf("Validate(empty debug_llm_dir) = %v, want nil", err)
	}
}

func TestMerge_DebugPrompt(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"debug_prompt": true})
	if !result.DebugPrompt {
		t.Error("DebugPrompt = false, want true after merge")
	}

	// Unset should not change (field not present)
	result2 := config.Merge(&cfg, map[string]any{})
	if result2.DebugPrompt {
		t.Error("DebugPrompt = true, want false when not in patch")
	}
}

func TestMerge_DebugRequest(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"debug_request": true})
	if !result.DebugRequest {
		t.Error("DebugRequest = false, want true after merge")
	}
}

func TestMerge_DebugLLMDir(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"debug_llm_dir": "/tmp/llm-debug"})
	if result.DebugLLMDir != "/tmp/llm-debug" {
		t.Errorf("DebugLLMDir = %q, want %q", result.DebugLLMDir, "/tmp/llm-debug")
	}

	// Clear it
	result2 := config.Merge(&cfg, map[string]any{"debug_llm_dir": ""})
	if result2.DebugLLMDir != "" {
		t.Errorf("DebugLLMDir = %q, want empty after clearing", result2.DebugLLMDir)
	}
}

func TestMerge_DebugPromptFromFormString(t *testing.T) {
	// Form data sends checkbox values as strings
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"debug_prompt": "true"})
	if !result.DebugPrompt {
		t.Error("DebugPrompt = false, want true (promoted from form string 'true')")
	}
}

func TestMerge_DebugRequestFromFormString(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"debug_request": "true"})
	if !result.DebugRequest {
		t.Error("DebugRequest = false, want true (promoted from form string 'true')")
	}
}

func TestDefaults_CompactionFields(t *testing.T) {
	def := config.Defaults()
	if !def.CompactionEnabled {
		t.Error("CompactionEnabled = false, want true by default")
	}
	if def.CompactionThresholdPercent != 90 {
		t.Errorf("CompactionThresholdPercent = %d, want 90", def.CompactionThresholdPercent)
	}
	if def.CompactionLowWaterPercent != 30 {
		t.Errorf("CompactionLowWaterPercent = %d, want 30", def.CompactionLowWaterPercent)
	}
}

func TestValidate_CompactionDefaultsAppliedWhenZero(t *testing.T) {
	// Config without compaction fields set should still validate (defaults applied)
	cfg := &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             "https://api.example.com",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
		CompactionEnabled:   true,
		// ThresholdPercent and LowWaterPercent left at 0
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate() = %v, want nil (defaults should be applied)", err)
	}
	// Validate mutates the struct to apply defaults
	if cfg.CompactionThresholdPercent != 90 {
		t.Errorf("CompactionThresholdPercent = %d after validate, want 90", cfg.CompactionThresholdPercent)
	}
	if cfg.CompactionLowWaterPercent != 30 {
		t.Errorf("CompactionLowWaterPercent = %d after validate, want 30", cfg.CompactionLowWaterPercent)
	}
}

func TestValidate_CompactionThresholdOutOfRange(t *testing.T) {
	cfg := &config.Config{
		Provider:                 "custom_openai",
		APIKey:                   "sk-test",
		BaseURL:                  "https://api.example.com",
		SessionTimeout:           30 * 60_000_000_000,
		CommandTimeout:           60_000_000_000,
		MaxTurns:                 25,
		ContextWindowTokens:      128000,
		CompactionThresholdPercent: 110,
		CompactionLowWaterPercent:  30,
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "compaction_threshold_percent") {
		t.Errorf("Validate() = %v, want error about compaction_threshold_percent out of range", err)
	}
}

func TestValidate_CompactionLowWaterOutOfRange(t *testing.T) {
	cfg := &config.Config{
		Provider:                 "custom_openai",
		APIKey:                   "sk-test",
		BaseURL:                  "https://api.example.com",
		SessionTimeout:           30 * 60_000_000_000,
		CommandTimeout:           60_000_000_000,
		MaxTurns:                 25,
		ContextWindowTokens:      128000,
		CompactionThresholdPercent: 90,
		CompactionLowWaterPercent:  5,
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "compaction_low_water_percent") {
		t.Errorf("Validate() = %v, want error about compaction_low_water_percent out of range", err)
	}
}

func TestValidate_CompactionLowWaterMustBeLessThanHigh(t *testing.T) {
	cfg := &config.Config{
		Provider:                 "custom_openai",
		APIKey:                   "sk-test",
		BaseURL:                  "https://api.example.com",
		SessionTimeout:           30 * 60_000_000_000,
		CommandTimeout:           60_000_000_000,
		MaxTurns:                 25,
		ContextWindowTokens:      128000,
		CompactionThresholdPercent: 50,
		CompactionLowWaterPercent:  55,
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "must be less than") {
		t.Errorf("Validate() = %v, want error about low < high", err)
	}
}

func TestValidate_CompactionLowWaterEqualToHigh(t *testing.T) {
	cfg := &config.Config{
		Provider:                 "custom_openai",
		APIKey:                   "sk-test",
		BaseURL:                  "https://api.example.com",
		SessionTimeout:           30 * 60_000_000_000,
		CommandTimeout:           60_000_000_000,
		MaxTurns:                 25,
		ContextWindowTokens:      128000,
		CompactionThresholdPercent: 60,
		CompactionLowWaterPercent:  60,
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "must be less than") {
		t.Errorf("Validate() = %v, want error about low < high", err)
	}
}

func TestDefaults_ContextWarningThreshold(t *testing.T) {
	def := config.Defaults()
	if def.ContextWarningThresholdPercent != 75 {
		t.Errorf("ContextWarningThresholdPercent = %d, want 75", def.ContextWarningThresholdPercent)
	}
}

func TestValidate_ContextWarningThresholdDefaultsAppliedWhenZero(t *testing.T) {
	cfg := &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             "https://api.example.com",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
		// ContextWarningThresholdPercent left at 0
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate() = %v, want nil (defaults should be applied)", err)
	}
	if cfg.ContextWarningThresholdPercent != 75 {
		t.Errorf("ContextWarningThresholdPercent = %d after validate, want 75", cfg.ContextWarningThresholdPercent)
	}
}

func TestValidate_ContextWarningThresholdOutOfRange(t *testing.T) {
	cfg := &config.Config{
		Provider:                      "custom_openai",
		APIKey:                        "sk-test",
		BaseURL:                       "https://api.example.com",
		SessionTimeout:                30 * 60_000_000_000,
		CommandTimeout:                60_000_000_000,
		MaxTurns:                      25,
		ContextWindowTokens:           128000,
		ContextWarningThresholdPercent: 200,
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "context_warning_threshold_percent") {
		t.Errorf("Validate() = %v, want error about context_warning_threshold_percent out of range", err)
	}
}

func TestMerge_ContextWarningThresholdPercent(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"context_warning_threshold_percent": "80"})
	if result.ContextWarningThresholdPercent != 80 {
		t.Errorf("ContextWarningThresholdPercent = %d, want 80", result.ContextWarningThresholdPercent)
	}
}

func TestMerge_CompactionEnabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	// Enable compaction (it's already true by default)
	result := config.Merge(&cfg, map[string]any{"compaction_enabled": "true"})
	if !result.CompactionEnabled {
		t.Error("CompactionEnabled = false, want true")
	}

	// Disable compaction
	result2 := config.Merge(&cfg, map[string]any{"compaction_enabled": "false"})
	if result2.CompactionEnabled {
		t.Error("CompactionEnabled = true after merge with false, want false")
	}
}

func TestMerge_CompactionThresholdPercent(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"compaction_threshold_percent": "85"})
	if result.CompactionThresholdPercent != 85 {
		t.Errorf("CompactionThresholdPercent = %d, want 85", result.CompactionThresholdPercent)
	}
}

func TestMerge_CompactionLowWaterPercent(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKey = "sk-test"

	result := config.Merge(&cfg, map[string]any{"compaction_low_water_percent": "25"})
	if result.CompactionLowWaterPercent != 25 {
		t.Errorf("CompactionLowWaterPercent = %d, want 25", result.CompactionLowWaterPercent)
	}
}

func TestLoad_SetsCompactionDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Save JSON without compaction fields (simulating old config from before compaction feature)
	oldConfig := `{
		"provider": "custom_openai",
		"api_key": "sk-test",
		"base_url": "https://api.example.com",
		"session_timeout": 1800000000000,
		"command_timeout": 60000000000,
		"max_turns": 25,
		"context_window_tokens": 128000
	}`
	if err := os.WriteFile(cfgPath, []byte(oldConfig), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if !cfg.CompactionEnabled {
		t.Error("CompactionEnabled = false, want true (default)")
	}
	if cfg.CompactionThresholdPercent != 90 {
		t.Errorf("CompactionThresholdPercent = %d, want 90 (default)", cfg.CompactionThresholdPercent)
	}
	if cfg.CompactionLowWaterPercent != 30 {
		t.Errorf("CompactionLowWaterPercent = %d, want 30 (default)", cfg.CompactionLowWaterPercent)
	}
}

func TestLoad_RoundTripsCompactionFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Save with all compaction fields explicitly set
	if err := config.Save(cfgPath, &config.Config{
		Provider:                  "custom_openai",
		APIKey:                    "sk-test",
		BaseURL:                   "https://api.example.com",
		SessionTimeout:            30 * 60_000_000_000,
		CommandTimeout:            60_000_000_000,
		MaxTurns:                  25,
		ContextWindowTokens:       128000,
		CompactionEnabled:         false,
		CompactionThresholdPercent: 75,
		CompactionLowWaterPercent:  20,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if cfg.CompactionEnabled {
		t.Error("CompactionEnabled = true, want false (saved explicitly)")
	}
	if cfg.CompactionThresholdPercent != 75 {
		t.Errorf("CompactionThresholdPercent = %d, want 75", cfg.CompactionThresholdPercent)
	}
	if cfg.CompactionLowWaterPercent != 20 {
		t.Errorf("CompactionLowWaterPercent = %d, want 20", cfg.CompactionLowWaterPercent)
	}
}

func TestLoad_PromotesCompactionEnabledEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Save config with compaction disabled
	if err := config.Save(cfgPath, &config.Config{
		Provider:                  "custom_openai",
		APIKey:                    "sk-test",
		BaseURL:                   "https://api.example.com",
		SessionTimeout:            30 * 60_000_000_000,
		CommandTimeout:            60_000_000_000,
		MaxTurns:                  25,
		ContextWindowTokens:       128000,
		CompactionEnabled:         false,
		CompactionThresholdPercent: 90,
		CompactionLowWaterPercent:  30,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	t.Setenv("EITRI_COMPACTION_ENABLED", "true")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if !cfg.CompactionEnabled {
		t.Error("CompactionEnabled = false after env var override, want true")
	}
}

func TestLoad_PromotesCompactionThresholdEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	if err := config.Save(cfgPath, &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             "https://api.example.com",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	t.Setenv("EITRI_COMPACTION_THRESHOLD_PERCENT", "75")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.CompactionThresholdPercent != 75 {
		t.Errorf("CompactionThresholdPercent = %d, want 75", cfg.CompactionThresholdPercent)
	}
}

func TestLoad_PromotesCompactionLowWaterEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	if err := config.Save(cfgPath, &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             "https://api.example.com",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	t.Setenv("EITRI_COMPACTION_LOW_WATER_PERCENT", "20")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.CompactionLowWaterPercent != 20 {
		t.Errorf("CompactionLowWaterPercent = %d, want 20", cfg.CompactionLowWaterPercent)
	}
}


