package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/sandbox"
)

// Config represents the Eitri configuration schema.
type Config struct {
	Provider                         string            `json:"provider"`
	APIKey                           string            `json:"api_key"`
	ProviderAuth                     json.RawMessage   `json:"provider_auth,omitempty"`
	AllowedReadPaths                 []string          `json:"allowed_read_paths,omitempty"`
	DisabledSkills                   []string          `json:"disabled_skills,omitempty"`
	UserEmail                        string            `json:"user_email,omitempty"`
	BaseURL                          string            `json:"base_url"`
	Model                            string            `json:"model"`
	ModelAPI                         string            `json:"model_api,omitempty"`
	ThinkingLevel                    string            `json:"thinking_level"`
	SystemPrompt                     string            `json:"system_prompt"`
	SessionTimeout                   int64             `json:"session_timeout"`
	CommandTimeout                   int64             `json:"command_timeout"`
	MaxTurns                         int               `json:"max_turns"`
	MaxOutputTokens                  int               `json:"max_output_tokens"`
	ContextWindowTokens              int               `json:"context_window_tokens"`
	ContextWindowOverridden          bool              `json:"context_window_overridden,omitempty"`
	MaxHistory                       int               `json:"max_history"`
	DebugPrompt                      bool              `json:"debug_prompt,omitempty"`  // was EITRI_DEBUG_PROMPT=1
	DebugRequest                     bool              `json:"debug_request,omitempty"` // was EITRI_DEBUG_REQUEST=1
	DebugLLMDir                      string            `json:"debug_llm_dir,omitempty"` // was EITRI_DEBUG_LLM_DIR
	CompactionEnabled                bool              `json:"compaction_enabled"`
	CompactionThresholdPercent       int               `json:"compaction_threshold_percent,omitempty"`
	CompactionLowWaterPercent        int               `json:"compaction_low_water_percent,omitempty"`
	CompactionMessageSizeThreshold   int               `json:"compaction_message_size_threshold,omitempty"`
	CompactionToolCallRetentionTurns int               `json:"compaction_tool_call_retention_turns,omitempty"`
	CompactionSalienceEnabled        bool              `json:"compaction_salience_enabled,omitempty"`
	ContextWarningThresholdPercent   int               `json:"context_warning_threshold_percent,omitempty"`
	Sandbox                          sandbox.Config    `json:"sandbox,omitempty"`
	ActivePersona                    string            `json:"active_persona,omitempty"`
	PersonaCatalog                   map[string]string `json:"persona_catalog,omitempty"`
	BrowserWsUrl                     string            `json:"browser_ws_url,omitempty"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	prof := provider.MustDescribe("opencode_go")
	return Config{
		Provider:                         prof.ID,
		BaseURL:                          prof.DefaultBaseURL,
		SessionTimeout:                   30 * 60_000_000_000, // 30 minutes in ns
		CommandTimeout:                   60 * 1_000_000_000,  // 60 seconds in ns
		MaxTurns:                         75,
		MaxOutputTokens:                  32000,
		ContextWindowTokens:              256000,
		MaxHistory:                       50,
		CompactionEnabled:                true,
		CompactionThresholdPercent:       90,
		CompactionLowWaterPercent:        30,
		CompactionMessageSizeThreshold:   2000,
		CompactionToolCallRetentionTurns: 5,
		CompactionSalienceEnabled:        true,
		ContextWarningThresholdPercent:   75,
		Sandbox:                          sandbox.DefaultConfig(),
		BrowserWsUrl:                     "ws://127.0.0.1:9222",
	}
}

// Load reads config from path. If file is missing, returns defaults without creating file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := Defaults()
			applyDefaults(&cfg)
			promoteEnvVars(&cfg)
			return &cfg, nil
		}
		return nil, err
	}

	// Check which top-level keys exist in the JSON so we can preserve
	// defaults for compaction fields that were not present in older config files.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cfg := Defaults()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// If the file did not contain compaction fields, keep the defaults.
	if _, ok := raw["compaction_enabled"]; !ok {
		cfg.CompactionEnabled = Defaults().CompactionEnabled
	}
	if _, ok := raw["compaction_threshold_percent"]; !ok {
		cfg.CompactionThresholdPercent = Defaults().CompactionThresholdPercent
	}
	if _, ok := raw["compaction_low_water_percent"]; !ok {
		cfg.CompactionLowWaterPercent = Defaults().CompactionLowWaterPercent
	}
	if _, ok := raw["context_warning_threshold_percent"]; !ok {
		cfg.ContextWarningThresholdPercent = Defaults().ContextWarningThresholdPercent
	}
	promoteEnvVars(&cfg)
	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults sets config fields that should have non-zero defaults when absent.
func applyDefaults(cfg *Config) {
	if cfg.ActivePersona == "" {
		cfg.ActivePersona = persona.GenericName
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = Defaults().MaxOutputTokens
	}
}

// promoteEnvVars promotes EITRI_DEBUG_* and EITRI_COMPACTION_* environment
// variables into config fields when the config field is its zero value,
// preserving backward compatibility for existing invocations that rely on env vars.
func promoteEnvVars(cfg *Config) {
	if !cfg.DebugPrompt {
		if os.Getenv("EITRI_DEBUG_PROMPT") == "1" {
			cfg.DebugPrompt = true
		}
	}
	if !cfg.DebugRequest {
		if os.Getenv("EITRI_DEBUG_REQUEST") == "1" {
			cfg.DebugRequest = true
		}
	}
	if cfg.DebugLLMDir == "" {
		if v := os.Getenv("EITRI_DEBUG_LLM_DIR"); v != "" {
			cfg.DebugLLMDir = v
		}
	}
	// Compaction env var overrides (always override, not just when zero)
	if v := os.Getenv("EITRI_COMPACTION_ENABLED"); v != "" {
		cfg.CompactionEnabled = v == "1" || v == "true"
	}
	if v := os.Getenv("EITRI_COMPACTION_THRESHOLD_PERCENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 50 && n <= 100 {
			cfg.CompactionThresholdPercent = n
		}
	}
	if v := os.Getenv("EITRI_COMPACTION_LOW_WATER_PERCENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 60 {
			cfg.CompactionLowWaterPercent = n
		}
	}
	if v := os.Getenv("EITRI_COMPACTION_MESSAGE_SIZE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.CompactionMessageSizeThreshold = n
		}
	}
	if v := os.Getenv("EITRI_CONTEXT_WARNING_THRESHOLD_PERCENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 95 {
			cfg.ContextWarningThresholdPercent = n
		}
	}
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

	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("base_url is required")
	}
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return fmt.Errorf("base_url is not a valid URL: %v", err)
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

	if cfg.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be non-negative, got %d", cfg.MaxOutputTokens)
	}

	if cfg.ContextWindowTokens < 1024 {
		return fmt.Errorf("context_window_tokens must be at least 1024, got %d", cfg.ContextWindowTokens)
	}

	if cfg.MaxHistory < 0 {
		return fmt.Errorf("max_history must be non-negative, got %d", cfg.MaxHistory)
	}

	if cfg.DebugLLMDir != "" {
		if !filepath.IsAbs(cfg.DebugLLMDir) {
			return fmt.Errorf("debug_llm_dir must be an absolute directory path, got %q", cfg.DebugLLMDir)
		}
	}

	// Apply defaults for compaction fields when they are zero (e.g. old configs without these fields).
	if cfg.CompactionThresholdPercent == 0 {
		cfg.CompactionThresholdPercent = 90
	}
	if cfg.CompactionLowWaterPercent == 0 {
		cfg.CompactionLowWaterPercent = 30
	}
	if cfg.CompactionThresholdPercent < 50 || cfg.CompactionThresholdPercent > 100 {
		return fmt.Errorf("compaction_threshold_percent must be between 50 and 100, got %d", cfg.CompactionThresholdPercent)
	}
	if cfg.CompactionLowWaterPercent < 10 || cfg.CompactionLowWaterPercent > 60 {
		return fmt.Errorf("compaction_low_water_percent must be between 10 and 60, got %d", cfg.CompactionLowWaterPercent)
	}
	if cfg.CompactionLowWaterPercent >= cfg.CompactionThresholdPercent {
		return fmt.Errorf("compaction_low_water_percent (%d) must be less than compaction_threshold_percent (%d)", cfg.CompactionLowWaterPercent, cfg.CompactionThresholdPercent)
	}

	if cfg.ContextWarningThresholdPercent == 0 {
		cfg.ContextWarningThresholdPercent = 75
	}
	if cfg.ContextWarningThresholdPercent < 10 || cfg.ContextWarningThresholdPercent > 95 {
		return fmt.Errorf("context_warning_threshold_percent must be between 10 and 95, got %d", cfg.ContextWarningThresholdPercent)
	}

	// Validate thinking_level field values
	if cfg.ThinkingLevel != "" && cfg.ThinkingLevel != "low" && cfg.ThinkingLevel != "medium" && cfg.ThinkingLevel != "high" {
		return fmt.Errorf("thinking_level must be one of \"\", \"low\", \"medium\", \"high\", got %q", cfg.ThinkingLevel)
	}

	// Validate persona catalog limit
	customCount := 0
	for name := range cfg.PersonaCatalog {
		if name != persona.GenericName {
			customCount++
		}
	}
	if customCount > persona.MaxCustomPersonas {
		return fmt.Errorf("persona catalog exceeds maximum of %d custom personas (not counting %q), got %d",
			persona.MaxCustomPersonas, persona.GenericName, customCount)
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
func Merge(base *Config, patch map[string]any) *Config {
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
		if arr, ok := v.([]any); ok {
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
	if v, ok := patch["thinking_level"]; ok {
		if s, ok := v.(string); ok {
			result.ThinkingLevel = s
		}
	}
	if v, ok := patch["session_timeout"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.SessionTimeout = int64(f)
		}
	}
	if v, ok := patch["command_timeout"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.CommandTimeout = int64(f)
		}
	}
	if v, ok := patch["max_turns"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.MaxTurns = int(f)
		}
	}
	if v, ok := patch["max_output_tokens"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.MaxOutputTokens = int(f)
		}
	}
	if v, ok := patch["context_window_tokens"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.ContextWindowTokens = int(f)
			result.ContextWindowOverridden = true
		}
	}
	if v, ok := patch["max_history"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.MaxHistory = int(f)
		}
	}
	if v, ok := patch["user_email"]; ok {
		if s, ok := v.(string); ok {
			result.UserEmail = s
		}
	}
	if v, ok := patch["debug_prompt"]; ok {
		result.DebugPrompt = parseBool(v)
	}
	if v, ok := patch["debug_request"]; ok {
		result.DebugRequest = parseBool(v)
	}
	if v, ok := patch["debug_llm_dir"]; ok {
		if s, ok := v.(string); ok {
			result.DebugLLMDir = s
		}
	}
	if v, ok := patch["sandbox_extra_writable_paths"]; ok {
		if s, ok := v.(string); ok {
			// Split by newlines, commas, or semicolons, trim spaces, remove empties.
			var paths []string
			for _, part := range strings.FieldsFunc(s, func(r rune) bool {
				return r == '\n' || r == ',' || r == ';'
			}) {
				part = strings.TrimSpace(part)
				if part != "" {
					paths = append(paths, part)
				}
			}
			result.Sandbox.ExtraWritablePaths = paths
		}
	}
	if v, ok := patch["sandbox_enabled"]; ok {
		if parseBool(v) {
			result.Sandbox.Profile = sandbox.ProfileDefault
		} else {
			result.Sandbox.Profile = sandbox.ProfileNone
		}
	}
	if v, ok := patch["compaction_enabled"]; ok {
		if parseBool(v) {
			result.CompactionEnabled = true
		} else {
			result.CompactionEnabled = false
		}
	}
	if v, ok := patch["compaction_threshold_percent"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.CompactionThresholdPercent = int(f)
		}
	}
	if v, ok := patch["compaction_low_water_percent"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.CompactionLowWaterPercent = int(f)
		}
	}
	if v, ok := patch["compaction_tool_call_retention_turns"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.CompactionToolCallRetentionTurns = int(f)
		}
	}
	if v, ok := patch["compaction_salience_enabled"]; ok {
		if parseBool(v) {
			result.CompactionSalienceEnabled = true
		} else {
			result.CompactionSalienceEnabled = false
		}
	}
	if v, ok := patch["context_warning_threshold_percent"]; ok {
		if f, ok := parseNumeric(v); ok {
			result.ContextWarningThresholdPercent = int(f)
		}
	}
	if v, ok := patch["active_persona"]; ok {
		if s, ok := v.(string); ok {
			result.ActivePersona = s
		}
	}
	if v, ok := patch["browser_ws_url"]; ok {
		if s, ok := v.(string); ok {
			result.BrowserWsUrl = s
		}
	}

	if providerChanged {
		if _, ok := patch["model"]; !ok {
			result.Model = ""
		}
		result.ModelAPI = ""
		if shouldResetBaseURLOnProviderSwitch(base.Provider, result.Provider, base.BaseURL, baseURLPatched, baseURLPatch) {
			if prof, err := provider.Describe(result.Provider); err == nil {
				result.BaseURL = prof.DefaultBaseURL
			}
		}
	}

	result.ProviderAuth = normalizeProviderAuth(result.Provider, result.APIKey, result.ProviderAuth)
	return &result
}

func clearAPIKeyRequested(v any) bool {
	s, ok := v.(string)
	if ok {
		return s == "true"
	}
	b, ok := v.(bool)
	return ok && b
}

func parseBool(v any) bool {
	s, ok := v.(string)
	if ok {
		return s == "true" || s == "1"
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

// parseNumeric attempts to parse a numeric value from an interface{}.
// It handles both float64 (from JSON unmarshalling) and string (from form-encoded data).
func parseNumeric(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
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
