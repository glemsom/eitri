package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glemsom/eitri/internal/config"
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

	// Defaults per SPEC §7.1
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
	if cfg.MaxTurns != 25 {
		t.Errorf("MaxTurns = %d, want 25", cfg.MaxTurns)
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
