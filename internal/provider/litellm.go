// Package provider owns LLM provider profile definitions, authentication
// handling, and model discovery.
package provider

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/voocel/litellm"
	"github.com/voocel/litellm/provider/anthropic"
	"github.com/voocel/litellm/provider/openai"
	"github.com/voocel/litellm/provider/openrouter"
)

// LitellmConfig carries the configuration needed to create a litellm provider
// and client. It mirrors the relevant fields of llm.AdapterConfig without
// importing that package, avoiding a circular dependency.
type LitellmConfig struct {
	ProviderID          string
	Model               string
	BaseURL             string
	APIKey              string
	OpenRouterRef       string
	OpenRouterTitle     string
	SupportsPromptCache bool
	RoundTripper        http.RoundTripper
}

// NewLitellmClient creates a *litellm.Client from a LitellmConfig by mapping
// Eitri's provider ID and model to the corresponding litellm provider config.
//
// Routing:
//   - opencode_go + qwen*/minimax* prefix → Anthropic provider
//   - opencode_go + any other model       → OpenAI provider
//   - custom_openai                        → OpenAI provider with user BaseURL
//   - openrouter                           → OpenRouter provider
//   - github_copilot                       → OpenAI provider with Copilot headers
//   - unknown                              → error
func NewLitellmClient(cfg LitellmConfig) (*litellm.Client, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	var prov litellm.Provider
	var err error

	switch cfg.ProviderID {
	case "opencode_go":
		prov, err = newOpenCodeGoProvider(cfg, baseURL)

	case "custom_openai":
		prov, err = openai.New(openai.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   baseURL,
			Transport: cfg.RoundTripper,
		})

	case "openrouter":
		prov, err = openrouter.New(openrouter.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   baseURL,
			Transport: cfg.RoundTripper,
			Headers: map[string]string{
				"HTTP-Referer": cfg.OpenRouterRef,
				"X-Title":      cfg.OpenRouterTitle,
			},
		})

	case "github_copilot":
		prov, err = openai.New(openai.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   baseURL,
			Transport: cfg.RoundTripper,
			Headers: map[string]string{
				"Editor-Version": "vscode/1.80.0",
				"User-Agent":     "GithubCopilot/1.100.0",
			},
		})

	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.ProviderID)
	}

	if err != nil {
		return nil, fmt.Errorf("create provider %q: %w", cfg.ProviderID, err)
	}

	return litellm.New(prov)
}

// newOpenCodeGoProvider routes the opencode_go provider to either Anthropic
// (for qwen*/minimax* models) or OpenAI (for everything else).
func newOpenCodeGoProvider(cfg LitellmConfig, baseURL string) (litellm.Provider, error) {
	if isAnthropicModel(cfg.Model) {
		// Anthropic provider adds /v1/messages itself; strip a trailing /v1
		// so we don't end up with /v1/v1/messages.
		cleaned := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
		return anthropic.New(anthropic.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   cleaned,
			Transport: cfg.RoundTripper,
		})
	}
	return openai.New(openai.Config{
		APIKey:    cfg.APIKey,
		BaseURL:   baseURL,
		Transport: cfg.RoundTripper,
	})
}

// isAnthropicModel returns true when the model prefix matches the
// OpenCode Go Anthropic-compatible route (qwen*, minimax*).
func isAnthropicModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "qwen") || strings.HasPrefix(lower, "minimax")
}
