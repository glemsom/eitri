package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents the Eitri configuration per SPEC §7.1.
type Config struct {
	Provider           string `json:"provider"`
	APIKey             string `json:"api_key"`
	BaseURL            string `json:"base_url"`
	Model              string `json:"model"`
	SystemPrompt       string `json:"system_prompt"`
	SessionTimeout     int64  `json:"session_timeout"`
	CommandTimeout     int64  `json:"command_timeout"`
	MaxTurns           int    `json:"max_turns"`
	ContextWindowTokens int   `json:"context_window_tokens"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Provider:            "opencode_go",
		BaseURL:             "https://opencode.ai/zen/go",
		SessionTimeout:      30 * 60_000_000_000,   // 30 minutes in ns
		CommandTimeout:      60 * 1_000_000_000,       // 60 seconds in ns
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
