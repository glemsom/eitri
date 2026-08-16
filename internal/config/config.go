// Package config handles Eitri's persistent local configuration: the JSON
// config file under the data directory (~/.eitri/config.json by default,
// path overridden by EITRI_CONFIG). It is created with defaults when absent,
// loaded on startup, and saved whenever settings change.
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
	// DefaultMaxTurns is the cap on loop iterations per run.
	DefaultMaxTurns = 250
	// DefaultCompactionFraction is the context-utilization trigger for
	// auto-compaction.
	DefaultCompactionFraction = 0.8
	// DefaultReasoningEffort is the per-session reasoning setting.
	DefaultReasoningEffort = "low"
	// DefaultTheme is the Markdown render theme when unset or unknown; "ascii" is
	// deliberately excluded from the supported set.
	DefaultTheme = "dark"
	// DefaultThinkingEnabled is whether chain-of-thought reasoning is on by
	// default; off yields requests with no thinking toggle/effort.
	DefaultThinkingEnabled = true
	// DefaultProvider and DefaultModel are the primary provider defaults.
	DefaultProvider = "opencode-go"
	DefaultModel    = "deepseek-v4-flash"
)

// CopilotConfig holds the GitHub Copilot device-flow credential state, persisted
// so a later batch run can reuse the TUI-established session without re-auth.
// Batch may transparently renew an expired access token
// via RefreshToken, but the interactive device-flow handshake is TUI-only.
type CopilotConfig struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is the unix-seconds expiry of AccessToken; 0 when unknown. Batch
	// refreshes when the access token is absent or past this time.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// OpenAIConfig holds a user-supplied OpenAI-compatible endpoint and API key
// (custom OpenAI provider). No device flow: key/setup only.
type OpenAIConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	Key     string `json:"key,omitempty"`
}

// Config is the persisted Eitri configuration. The primary provider's key
// (OpenCode Go) is delivered via the OPENCODE_API_KEY environment variable; the
// Copilot device-flow tokens and the custom-OpenAI endpoint/key are stored here
// because they are user-configured and reused across runs.
type Config struct {
	Provider           string        `json:"provider"`
	Model              string        `json:"model"`
	ReasoningEffort    string        `json:"reasoning_effort"`
	ThinkingEnabled    bool          `json:"thinking_enabled"`
	MaxTurns           int           `json:"max_turns"`
	CompactionFraction float64       `json:"compaction_fraction"`
	ExtraWritablePaths []string      `json:"extra_writable_paths,omitempty"`
	Theme              string        `json:"theme"`
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
		CompactionFraction: DefaultCompactionFraction,
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
	// A file that never saved a theme field keeps the shipped default: an absent
	// or empty theme means "dark", never an error.
	if cfg.Theme == "" {
		cfg.Theme = DefaultTheme
	}
	// A file that never saved a reasoning_effort field keeps the shipped
	// default rather than the empty zero value.
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
