package provider

import (
	"fmt"
	"net/http"

	"github.com/glemsom/eitri/internal/config"
)

// Provider family identifiers, matching the documented families surfaced in
// the Settings surface. The saved config.Provider value
// selects which transport/credential a run uses, honored across TUI and batch.
const (
	ProviderOpenCodeGo   = "opencode-go"
	ProviderCopilot      = "github-copilot"
	ProviderCustomOpenAI = "custom-openai"
)

// Default endpoints for the non-default provider families.
const (
	// DefaultCopilotURL is the GitHub Copilot Chat-Completions endpoint.
	DefaultCopilotURL = "https://api.githubcopilot.com/chat/completions"
	// DefaultOpenCodeURL is the primary OpenCode Go Chat-Completions endpoint.
	DefaultOpenCodeURL = "https://opencode.ai/zen/go/v1/chat/completions"
)

// apiKeyOrDefault returns key, or a sentinel so the client is still wired and a
// clean HTTP 401 surfaces rather than a misleading empty-credential request.
func apiKeyOrDefault(key string) string {
	if key != "" {
		return key
	}
	return "not-configured"
}

// ProviderEnv carries the environment-derived credential and the seams the
// provider factory needs so routing is testable without real network. OpenCode
// Go's key is delivered by env; Copilot's refresh/persist are wired by the app.
type ProviderEnv struct {
	// OpenCodeKey is the OPENCODE_API_KEY for the primary provider.
	OpenCodeKey string
	// OpenCodeURL overrides the primary endpoint (empty → DefaultOpenCodeURL).
	OpenCodeURL string
	// HTTP backs the Copilot provider's HTTP client (nil → http.DefaultClient).
	HTTP *http.Client

	// CopilotRefresh renews a Copilot credential non-interactively (nil → no
	// refresh path; an expired token errors with ErrReauthRequired).
	CopilotRefresh RefreshFunc
	// CopilotPersist saves a renewed Copilot token set back to config (nil skips).
	CopilotPersist func(config.CopilotConfig) error
}

// FromConfig builds the Provider the saved config selects — opencode-go,
// github-copilot, or custom-openai — routing through the shared
// Chat-Completions dialect seam (one canonical per-dialect serializer, no
// per-provider copies). An unknown provider or a custom-openai selection without
// a configured endpoint is an explicit error.
func FromConfig(cfg config.Config, env ProviderEnv) (Provider, error) {
	switch cfg.Provider {
	case ProviderOpenCodeGo:
		url := env.OpenCodeURL
		if url == "" {
			url = DefaultOpenCodeURL
		}
		return NewOpenAICompatible(apiKeyOrDefault(env.OpenCodeKey), url), nil

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
