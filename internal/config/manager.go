package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

// Config represents the Eitri configuration per SPEC §7.1.
type Config struct {
	Provider            string `json:"provider"`
	APIKey              string `json:"api_key"`
	BaseURL             string `json:"base_url"`
	Model               string `json:"model"`
	SystemPrompt        string `json:"system_prompt"`
	SessionTimeout      int64  `json:"session_timeout"`
	CommandTimeout      int64  `json:"command_timeout"`
	MaxTurns            int    `json:"max_turns"`
	ContextWindowTokens int    `json:"context_window_tokens"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	prof := provider.MustGet("opencode_go")
	return Config{
		Provider:            prof.ID,
		BaseURL:             prof.DefaultBaseURL,
		SessionTimeout:      30 * 60_000_000_000, // 30 minutes in ns
		CommandTimeout:      60 * 1_000_000_000,  // 60 seconds in ns
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}
}

// Load reads config from path. If file is missing, returns defaults without creating file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := Defaults()
			return &cfg, nil
		}
		return nil, err
	}

	cfg := Defaults()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes config to path, creating parent directories with secure permissions.
func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Append newline
	data = append(data, '\n')

	// Write atomically via temp file + rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Validate checks field-level constraints per SPEC §7.2.
// Returns a descriptive error for the first violation found.
func Validate(cfg *Config) error {
	prof, err := provider.Get(cfg.Provider)
	if err != nil {
		return fmt.Errorf("provider must be one of %s, got %q", strings.Join(provider.IDs(), ", "), cfg.Provider)
	}

	if prof.APIKeyRequired && cfg.APIKey == "" {
		return fmt.Errorf("%s is required for provider %q", prof.RequiredCredentialName(), cfg.Provider)
	}

	if cfg.BaseURL != "" {
		if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
			return fmt.Errorf("base_url is not a valid URL: %v", err)
		}
	}

	if cfg.SessionTimeout < 60_000_000_000 { // 1 minute in ns
		return fmt.Errorf("session_timeout must be at least 1 minute (60000000000 ns), got %d", cfg.SessionTimeout)
	}

	if cfg.CommandTimeout < 1_000_000_000 { // 1 second in ns
		return fmt.Errorf("command_timeout must be at least 1 second (1000000000 ns), got %d", cfg.CommandTimeout)
	}

	if cfg.MaxTurns < 1 {
		return fmt.Errorf("max_turns must be at least 1, got %d", cfg.MaxTurns)
	}

	if cfg.ContextWindowTokens < 1024 {
		return fmt.Errorf("context_window_tokens must be at least 1024, got %d", cfg.ContextWindowTokens)
	}

	return nil
}

// MaskAPIKey returns a masked version of the API key per SPEC §7.2:
// first 5 chars + "..." + last 3 chars. If key is too short, returns as-is.
func MaskAPIKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:5] + "..." + key[len(key)-3:]
}

// Merge applies a partial patch (from JSON unmarshalling) onto a base Config.
// Only recognized fields are overridden; unknown fields are ignored.
// clear_api_key=true explicitly empties the API key.
func Merge(base *Config, patch map[string]interface{}) *Config {
	result := *base // shallow copy

	if v, ok := patch["provider"]; ok {
		if s, ok := v.(string); ok {
			result.Provider = s
		}
	}
	if v, ok := patch["api_key"]; ok {
		if s, ok := v.(string); ok {
			result.APIKey = s
		}
	}
	// clear_api_key checkbox: if explicitly true, clear the key
	if v, ok := patch["clear_api_key"]; ok {
		if s, ok := v.(string); ok && s == "true" {
			result.APIKey = ""
		}
	}
	if v, ok := patch["base_url"]; ok {
		if s, ok := v.(string); ok {
			result.BaseURL = s
		}
	}
	if v, ok := patch["model"]; ok {
		if s, ok := v.(string); ok {
			result.Model = s
		}
	}
	if v, ok := patch["system_prompt"]; ok {
		if s, ok := v.(string); ok {
			result.SystemPrompt = s
		}
	}
	if v, ok := patch["session_timeout"]; ok {
		if f, ok := v.(float64); ok {
			result.SessionTimeout = int64(f)
		}
	}
	if v, ok := patch["command_timeout"]; ok {
		if f, ok := v.(float64); ok {
			result.CommandTimeout = int64(f)
		}
	}
	if v, ok := patch["max_turns"]; ok {
		if f, ok := v.(float64); ok {
			result.MaxTurns = int(f)
		}
	}
	if v, ok := patch["context_window_tokens"]; ok {
		if f, ok := v.(float64); ok {
			result.ContextWindowTokens = int(f)
		}
	}

	return &result
}
