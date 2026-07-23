package llm

import (
	"fmt"
	"strings"
)

// NewLLMService creates an LLMService adapter based on provider routing rules.
//
// Routing:
//   - opencode_go + qwen*/minimax* prefix → Anthropic adapter
//   - opencode_go + any other model       → OpenAI adapter
//   - openrouter                           → OpenRouter adapter
//   - github_copilot                       → GitHub Copilot adapter
//   - custom_openai                        → OpenAI adapter with user BaseURL
//   - unknown                              → error
func NewLLMService(cfg AdapterConfig) (LLMService, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	switch cfg.ProviderID {
	case "opencode_go":
		if isAnthropicModel(cfg.Model) {
			return NewAnthropic(cfg.Model, baseURL, cfg.APIKey, cfg.RoundTripper), nil
		}
		return NewOpenAI(cfg.Model, baseURL, cfg.APIKey, cfg.RoundTripper), nil

	case "custom_openai":
		return NewOpenAI(cfg.Model, baseURL, cfg.APIKey, cfg.RoundTripper), nil

	case "openrouter":
		return NewOpenRouter(cfg.Model, baseURL, cfg.APIKey, cfg.OpenRouterRef, cfg.OpenRouterTitle, cfg.RoundTripper), nil

	case "github_copilot":
		return NewGitHubCopilot(cfg.Model, baseURL, cfg.APIKey, cfg.RoundTripper), nil

	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.ProviderID)
	}
}

// isAnthropicModel returns true when the model prefix matches the
// OpenCode Go Anthropic-compatible route (qwen*, minimax*).
func isAnthropicModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "qwen") || strings.HasPrefix(lower, "minimax")
}
