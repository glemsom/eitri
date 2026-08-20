// Package config handles Eitri's persistent local configuration: the JSON config file under the data directory (~/.eitri/config.json by default, path overridden by EITRI_CONFIG).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glemsom/eitri/internal/constants"
)

// Defaults for session and provider behavior.
const (
	DefaultMaxTurns        = 250
	DefaultReasoningEffort = "low"
	DefaultTheme           = "dark"
	DefaultThinkingEnabled = true
	DefaultProvider        = "opencode-go"
	DefaultModel           = "deepseek-v4-flash"
)

// CopilotConfig holds the GitHub Copilot device-flow credential state, persisted so a later batch run can reuse the TUI-established session without re-auth.
type CopilotConfig struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

// OpenAIConfig holds a user-supplied OpenAI-compatible endpoint and API key (custom OpenAI provider).
type OpenAIConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	Key     string `json:"key,omitempty"`
}

// Config is the persisted Eitri configuration.
type Config struct {
	Provider           string        `json:"provider"`
	Model              string        `json:"model"`
	ReasoningEffort    string        `json:"reasoning_effort"`
	ThinkingEnabled    bool          `json:"thinking_enabled"`
	MaxTurns           int           `json:"max_turns"`
	CompactionFraction float64       `json:"compaction_fraction"`
	ExtraWritablePaths []string      `json:"extra_writable_paths,omitempty"`
	Theme              string        `json:"theme"`
	RailWidth          int           `json:"rail_width,omitempty"`
	Copilot            CopilotConfig `json:"copilot,omitempty"`
	CustomOpenAI       OpenAIConfig  `json:"custom_openai,omitempty"`
}

// Default returns a config populated with Eitri's defaults.
func Default() Config {
	return Config{
		Provider:           DefaultProvider,
		Model:              DefaultModel,
		ReasoningEffort:    DefaultReasoningEffort,
		ThinkingEnabled:    DefaultThinkingEnabled,
		MaxTurns:           DefaultMaxTurns,
		CompactionFraction: constants.DefaultCompactionFraction,
		Theme:              DefaultTheme,
	}
}

// Load reads the config file at path, creating it with defaults when absent.
func Load(path string) (Config, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := Save(cfg, path); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Theme == "" {
		cfg.Theme = DefaultTheme
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = DefaultReasoningEffort
	}
	return cfg, nil
}

// Save writes cfg to path as JSON, creating parent directories as needed.
func Save(cfg Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
