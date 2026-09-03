package provider

import (
	"fmt"
	"net/http"

	"github.com/glemsom/eitri/internal/config"
)

// Default endpoints for the non-default provider families.
const (
	DefaultCopilotURL  = "https://api.githubcopilot.com/chat/completions"
	DefaultOpenCodeURL = "https://opencode.ai/zen/go/v1/chat/completions"
)

// apiKeyOrDefault returns key, or a sentinel so the client is still wired and a clean HTTP 401 surfaces rather than a misleading empty-credential request.
func apiKeyOrDefault(key string) string {
	if key != "" {
		return key
	}
	return "not-configured"
}

// ProviderEnv carries the environment-derived credential and the seams the provider factory needs so routing is testable without real network.
type ProviderEnv struct {
	OpenCodeKey string
	OpenCodeURL string
	HTTP        *http.Client

	CopilotRefresh RefreshFunc
	CopilotPersist func(config.CopilotConfig) error
}

// FromConfig builds the Provider the saved config selects — opencode-go, github-copilot, or custom-openai — routing through the shared Chat-Completions dialect seam (canonical tool re-expression and request shaping behind one interface, no per-provider copies).
func FromConfig(cfg config.Config, env ProviderEnv) (Provider, error) {
	switch ProviderID(cfg.Provider) {
	case ProviderOpenCodeGo:
		url := env.OpenCodeURL
		if url == "" {
			url = DefaultOpenCodeURL
		}
		return NewOpenCodeGo(apiKeyOrDefault(env.OpenCodeKey), url), nil

	case ProviderCustomOpenAI:
		if cfg.CustomOpenAI.BaseURL == "" || cfg.CustomOpenAI.Key == "" {
			return nil, fmt.Errorf("custom-openai provider selected but no base URL/key configured (set them in Settings)")
		}
		return NewOpenAICompatible(cfg.CustomOpenAI.Key, cfg.CustomOpenAI.BaseURL), nil

	case ProviderCopilot:
		return NewCopilot(cfg.Copilot, DefaultCopilotURL, env.HTTP, env.CopilotRefresh, env.CopilotPersist), nil

	default:
		return nil, fmt.Errorf("unknown provider %q; supported: opencode-go, github-copilot, custom-openai", cfg.Provider)
	}
}
