// Package config handles Eitri's persistent local configuration: the JSON config file under the data directory (~/.eitri/config.json by default, path overridden by EITRI_CONFIG).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// OpenCodeGoConfig holds the OpenCode Go API key, persisted so a TUI-established credential is reused by later runs.
type OpenCodeGoConfig struct {
	Key string `json:"key,omitempty"`
}

// OpenAIConfig holds a user-supplied OpenAI-compatible endpoint and API key (custom OpenAI provider).
type OpenAIConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	Key     string `json:"key,omitempty"`
}

// Config is the persisted Eitri configuration.
type Config struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	ThinkingEnabled bool   `json:"thinking_enabled"`
	// CoTCollapsedByDefault and ToolResultsCollapsedByDefault are the
	// tool results render as hints/one-liners until expanded, so a large CoT
	// never pushes tool calls out of view.
	CoTCollapsedByDefault         bool             `json:"cot_collapsed_by_default"`
	ToolResultsCollapsedByDefault bool             `json:"tool_results_collapsed_by_default"`
	MaxTurns                      int              `json:"max_turns"`
	ContextOverflowRecovery       bool             `json:"context_overflow_recovery"`
	ExtraWritablePaths            []string         `json:"extra_writable_paths,omitempty"`
	Theme                         string           `json:"theme"`
	RailWidth                     int              `json:"rail_width,omitempty"`
	Copilot                       CopilotConfig    `json:"copilot,omitempty"`
	OpenCodeGo                    OpenCodeGoConfig `json:"opencode_go,omitempty"`
	CustomOpenAI                  OpenAIConfig     `json:"custom_openai,omitempty"`
}

// Default returns a config populated with Eitri's defaults.
func Default() Config {
	return Config{
		Provider:                      DefaultProvider,
		Model:                         DefaultModel,
		ReasoningEffort:               DefaultReasoningEffort,
		ThinkingEnabled:               DefaultThinkingEnabled,
		CoTCollapsedByDefault:         true,
		ToolResultsCollapsedByDefault: true,
		MaxTurns:                      DefaultMaxTurns,
		ContextOverflowRecovery:       true,
		Theme:                         DefaultTheme,
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
	// Start from current defaults so legacy files inherit fields added after
	// they were written. Unmarshal overwrites only keys present in the file,
	// preserving explicit false and zero choices.
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path as JSON, creating parent directories as needed.
func Save(cfg Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	if dir, err := os.Open(dir); err != nil {
		return fmt.Errorf("open config dir: %w", err)
	} else {
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return fmt.Errorf("sync config dir: %w", err)
		}
	}
	return nil
}
