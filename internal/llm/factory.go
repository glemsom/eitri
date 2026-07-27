package llm

import (
	"github.com/glemsom/eitri/internal/provider"
)

// NewLLMService creates an LLMService adapter based on provider routing rules.
//
// Routing:
//   - opencode_go + qwen*/minimax* prefix → Anthropic adapter (via litellm)
//   - opencode_go + any other model       → OpenAI adapter (via litellm)
//   - openrouter                           → OpenRouter adapter (via litellm)
//   - github_copilot                       → GitHub Copilot adapter (via litellm)
//   - custom_openai                        → OpenAI adapter with user BaseURL (via litellm)
//   - unknown                              → error
func NewLLMService(cfg AdapterConfig) (LLMService, error) {
	client, err := provider.NewLitellmClient(provider.LitellmConfig{
		ProviderID:      cfg.ProviderID,
		Model:           cfg.Model,
		BaseURL:         cfg.BaseURL,
		APIKey:          cfg.APIKey,
		OpenRouterRef:   cfg.OpenRouterRef,
		OpenRouterTitle: cfg.OpenRouterTitle,
		RoundTripper:    cfg.RoundTripper,
		DebugPrompt:     cfg.DebugPrompt,
		DebugRequest:    cfg.DebugRequest,
		DebugLLMDir:     cfg.DebugLLMDir,
	})
	if err != nil {
		return nil, err
	}
	return NewBridge(client), nil
}
