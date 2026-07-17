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

// Config represents the Eitri configuration schema.
type Config struct {
	Provider            string          `json:"provider"`
	APIKey              string          `json:"api_key"`
	ProviderAuth        json.RawMessage `json:"provider_auth,omitempty"`
	AllowedReadPaths    []string        `json:"allowed_read_paths,omitempty"`
	BaseURL             string          `json:"base_url"`
	Model               string          `json:"model"`
	SystemPrompt        string          `json:"system_prompt"`
	SessionTimeout      int64           `json:"session_timeout"`
	CommandTimeout      int64           `json:"command_timeout"`
	MaxTurns            int             `json:"max_turns"`
	ContextWindowTokens int             `json:"context_window_tokens"`
	MaxHistory          int             `json:"max_history"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	prof := provider.MustDescribe("opencode_go")
	return Config{
		Provider:            prof.ID,
		BaseURL:             prof.DefaultBaseURL,
		SessionTimeout:      30 * 60_000_000_000, // 30 minutes in ns
		CommandTimeout:      60 * 1_000_000_000,  // 60 seconds in ns
		MaxTurns:            25,
		ContextWindowTokens: 256000,
		MaxHistory:          50,
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

// Validate checks field-level constraints.
// Returns a descriptive error for the first violation found.
func Validate(cfg *Config) error {
	if _, err := provider.Describe(cfg.Provider); err != nil {
		return fmt.Errorf("provider must be one of %s, got %q", strings.Join(provider.IDs(), ", "), cfg.Provider)
	}
	if err := provider.ValidateCredentials(cfg.Provider, cfg.APIKey, cfg.ProviderAuth); err != nil {
		return err
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

	if cfg.MaxHistory < 0 {
		return fmt.Errorf("max_history must be non-negative, got %d", cfg.MaxHistory)
	}

	return nil
}

// ValidateSelectedModel checks that cfg.Model is present in live-discovered models.
func ValidateSelectedModel(cfg *Config, models []string) error {
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("model is required; choose one from discovered models")
	}
	for _, model := range models {
		if model == cfg.Model {
			return nil
		}
	}
	return fmt.Errorf("selected model %q is no longer available; choose another discovered model", cfg.Model)
}

// MaskAPIKey returns a masked version of the API key:
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
	result.ProviderAuth = cloneRawMessage(base.ProviderAuth)
	providerChanged := false
	baseURLPatched := false
	baseURLPatch := ""

	if v, ok := patch["provider"]; ok {
		if s, ok := v.(string); ok {
			providerChanged = s != base.Provider
			result.Provider = s
		}
	}
	if v, ok := patch["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" {
			result.APIKey = s
		}
	}
	// clear_api_key checkbox: if explicitly true, clear the key
	if v, ok := patch["clear_api_key"]; ok && clearAPIKeyRequested(v) {
		result.APIKey = ""
	}
	if v, ok := patch["base_url"]; ok {
		if s, ok := v.(string); ok {
			baseURLPatched = true
			baseURLPatch = s
			result.BaseURL = s
		}
	}
	if v, ok := patch["model"]; ok {
		if s, ok := v.(string); ok {
			result.Model = s
		}
	}
	if v, ok := patch["allowed_read_paths"]; ok {
		if arr, ok := v.([]interface{}); ok {
			paths := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					paths = append(paths, s)
				}
			}
			result.AllowedReadPaths = paths
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
	if v, ok := patch["max_history"]; ok {
		if f, ok := v.(float64); ok {
			result.MaxHistory = int(f)
		}
	}

	if providerChanged {
		if _, ok := patch["model"]; !ok {
			result.Model = ""
		}
		if shouldResetBaseURLOnProviderSwitch(base.Provider, result.Provider, base.BaseURL, baseURLPatched, baseURLPatch) {
			if prof, err := provider.Describe(result.Provider); err == nil {
				result.BaseURL = prof.DefaultBaseURL
			}
		}
	}

	result.ProviderAuth = normalizeProviderAuth(result.Provider, result.APIKey, result.ProviderAuth)
	return &result
}

func clearAPIKeyRequested(v interface{}) bool {
	s, ok := v.(string)
	if ok {
		return s == "true"
	}
	b, ok := v.(bool)
	return ok && b
}

func normalizeProviderAuth(providerID, apiKey string, raw json.RawMessage) json.RawMessage {
	normalized, err := provider.NormalizeConfigAuthState(providerID, apiKey, raw)
	if err != nil {
		return cloneRawMessage(raw)
	}
	return normalized
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	clone := make([]byte, len(raw))
	copy(clone, raw)
	return json.RawMessage(clone)
}

func shouldResetBaseURLOnProviderSwitch(oldProviderID, newProviderID, oldBaseURL string, baseURLPatched bool, baseURLPatch string) bool {
	oldProf, oldErr := provider.Describe(oldProviderID)
	if _, err := provider.Describe(newProviderID); err != nil {
		return false
	}

	oldBaseWasDefault := oldErr == nil && oldBaseURL == oldProf.DefaultBaseURL
	oldBaseWasEmpty := oldBaseURL == ""

	if !baseURLPatched {
		return oldBaseWasEmpty || oldBaseWasDefault
	}
	if baseURLPatch == "" {
		return true
	}
	return oldBaseWasDefault && baseURLPatch == oldBaseURL
}
