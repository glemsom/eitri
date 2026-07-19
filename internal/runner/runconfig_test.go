package runner

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

func TestRunConfig_HasAllAgreedFields(t *testing.T) {
	// Verify RunConfig compiles and is addressable.
	rc := &RunConfig{
		ProviderID:          "opencode_go",
		BaseURL:             "https://api.example.com",
		APIKey:              "sk-abc123",
		ModelName:           "test-model",
		SystemPrompt:        "You are Eitri.",
		MaxTurns:            25,
		MaxHistory:          50,
		AllowedReadPaths:    []string{"/workspace/shared"},
		ProviderAuth:        json.RawMessage(`{"token":"xyz"}`),
		Workspace:           "/home/user/project",
		CmdTimeout:          30 * time.Second,
		ContextWindowTokens: 128000,
	}

	if rc.ProviderID != "opencode_go" {
		t.Errorf("ProviderID = %q, want opencode_go", rc.ProviderID)
	}
	if rc.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q", rc.BaseURL)
	}
	if rc.APIKey != "sk-abc123" {
		t.Errorf("APIKey = %q", rc.APIKey)
	}
	if rc.ModelName != "test-model" {
		t.Errorf("ModelName = %q", rc.ModelName)
	}
	if rc.SystemPrompt != "You are Eitri." {
		t.Errorf("SystemPrompt = %q", rc.SystemPrompt)
	}
	if rc.MaxTurns != 25 {
		t.Errorf("MaxTurns = %d", rc.MaxTurns)
	}
	if rc.MaxHistory != 50 {
		t.Errorf("MaxHistory = %d", rc.MaxHistory)
	}
	if len(rc.AllowedReadPaths) != 1 || rc.AllowedReadPaths[0] != "/workspace/shared" {
		t.Errorf("AllowedReadPaths = %v", rc.AllowedReadPaths)
	}
	if string(rc.ProviderAuth) != `{"token":"xyz"}` {
		t.Errorf("ProviderAuth = %s", string(rc.ProviderAuth))
	}
	if rc.Workspace != "/home/user/project" {
		t.Errorf("Workspace = %q", rc.Workspace)
	}
	if rc.CmdTimeout != 30*time.Second {
		t.Errorf("CmdTimeout = %v", rc.CmdTimeout)
	}
	if rc.ContextWindowTokens != 128000 {
		t.Errorf("ContextWindowTokens = %d", rc.ContextWindowTokens)
	}
}

func TestFromConfig_MapsAllFields(t *testing.T) {
	cfg := &config.Config{
		Provider:            "github_copilot",
		BaseURL:             "https://copilot.example.com",
		APIKey:              "gh-key",
		Model:               "gpt-4",
		SystemPrompt:        "Be helpful.",
		MaxTurns:            50,
		MaxHistory:          100,
		ContextWindowTokens: 64000,
		AllowedReadPaths:    []string{"/shared"},
		ProviderAuth:        json.RawMessage(`{"tenant":"my-org"}`),
	}

	rc := FromConfig(cfg, "/workspace/proj", 60*time.Second)

	if rc.ProviderID != "github_copilot" {
		t.Errorf("ProviderID = %q, want github_copilot", rc.ProviderID)
	}
	if rc.BaseURL != "https://copilot.example.com" {
		t.Errorf("BaseURL = %q", rc.BaseURL)
	}
	if rc.APIKey != "gh-key" {
		t.Errorf("APIKey = %q", rc.APIKey)
	}
	if rc.ModelName != "gpt-4" {
		t.Errorf("ModelName = %q", rc.ModelName)
	}
	if rc.SystemPrompt != "Be helpful." {
		t.Errorf("SystemPrompt = %q", rc.SystemPrompt)
	}
	if rc.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d", rc.MaxTurns)
	}
	if rc.MaxHistory != 100 {
		t.Errorf("MaxHistory = %d", rc.MaxHistory)
	}
	if rc.ContextWindowTokens != 64000 {
		t.Errorf("ContextWindowTokens = %d", rc.ContextWindowTokens)
	}
	if len(rc.AllowedReadPaths) != 1 || rc.AllowedReadPaths[0] != "/shared" {
		t.Errorf("AllowedReadPaths = %v", rc.AllowedReadPaths)
	}
	if string(rc.ProviderAuth) != `{"tenant":"my-org"}` {
		t.Errorf("ProviderAuth = %s", string(rc.ProviderAuth))
	}
	if rc.Workspace != "/workspace/proj" {
		t.Errorf("Workspace = %q", rc.Workspace)
	}
	if rc.CmdTimeout != 60*time.Second {
		t.Errorf("CmdTimeout = %v, want 60s", rc.CmdTimeout)
	}
}

func TestFromConfig_DefaultsEmptySystemPrompt(t *testing.T) {
	cfg := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "qwen2",
	}

	rc := FromConfig(cfg, "/ws", 10*time.Second)

	if rc.SystemPrompt == "" {
		t.Fatal("SystemPrompt should not be empty when config has empty SystemPrompt")
	}
}

func TestFromConfig_PreservesConfigSystemPrompt(t *testing.T) {
	cfg := &config.Config{
		Provider:     "opencode_go",
		BaseURL:      "http://localhost",
		APIKey:       "key",
		Model:        "qwen2",
		SystemPrompt: "Custom prompt.",
	}

	rc := FromConfig(cfg, "/ws", 10*time.Second)

	if rc.SystemPrompt != "Custom prompt." {
		t.Errorf("SystemPrompt = %q, want 'Custom prompt.'", rc.SystemPrompt)
	}
}

func TestFromConfig_ZeroValueProtection(t *testing.T) {
	cfg := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "qwen2",
		// MaxTurns, MaxHistory, ContextWindowTokens are zero
	}

	rc := FromConfig(cfg, "/ws", 10*time.Second)

	// Zero values should be preserved (caller or StartRun handles defaults)
	if rc.MaxTurns != 0 {
		t.Errorf("MaxTurns = %d, want 0 (zero-value protection)", rc.MaxTurns)
	}
	if rc.MaxHistory != 0 {
		t.Errorf("MaxHistory = %d, want 0 (zero-value protection)", rc.MaxHistory)
	}
	if rc.ContextWindowTokens != 0 {
		t.Errorf("ContextWindowTokens = %d, want 0 (zero-value protection)", rc.ContextWindowTokens)
	}
}

func TestFromConfig_NilProviderAuth(t *testing.T) {
	cfg := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "qwen2",
	}

	rc := FromConfig(cfg, "/ws", 10*time.Second)
	if rc.ProviderAuth != nil {
		t.Errorf("ProviderAuth = %s, want nil", string(rc.ProviderAuth))
	}
}

func TestFromConfig_NilAllowedReadPaths(t *testing.T) {
	cfg := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://localhost",
		APIKey:   "key",
		Model:    "qwen2",
	}

	rc := FromConfig(cfg, "/ws", 10*time.Second)
	if rc.AllowedReadPaths != nil {
		t.Errorf("AllowedReadPaths = %v, want nil", rc.AllowedReadPaths)
	}
}
