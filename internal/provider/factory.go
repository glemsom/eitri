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

// ProviderEnv carries the environment-derived credential and the seams the provider factory needs so routing is testable without real network.
type ProviderEnv struct {
	OpenCodeKey string
	OpenCodeURL string
	HTTP        *http.Client

	CopilotRefresh RefreshFunc
	CopilotPersist func(config.CopilotConfig) error
}

func FromConfig(cfg config.Config, env ProviderEnv) (Provider, error) {
	switch ProviderID(cfg.Provider) {
	case ProviderOpenCodeGo:
		url := env.OpenCodeURL
		if url == "" {
			url = DefaultOpenCodeURL
		}
		if env.OpenCodeKey == "" {
			return nil, fmt.Errorf("opencode-go provider selected but OPENCODE_API_KEY is not set")
		}
		return NewOpenCodeGo(env.OpenCodeKey, url), nil

	case ProviderCustomOpenAI:
		if cfg.CustomOpenAI.BaseURL == "" {
			return nil, fmt.Errorf("custom-openai provider selected but no base URL configured (set it in Settings)")
		}
		return NewOpenAICompatible(cfg.CustomOpenAI.Key, cfg.CustomOpenAI.BaseURL), nil

	case ProviderCopilot:
		return NewCopilot(cfg.Copilot, DefaultCopilotURL, env.HTTP, env.CopilotRefresh, env.CopilotPersist), nil

	default:
		return nil, fmt.Errorf("unknown provider %q; supported: opencode-go, github-copilot, custom-openai", cfg.Provider)
	}
}
