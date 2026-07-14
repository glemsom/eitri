package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/adk/v2/model"
)

// ChatRequest describes provider-language inputs for chat-model creation.
type ChatRequest struct {
	ProviderID   string
	BaseURL      string
	APIKey       string
	ProviderAuth json.RawMessage
	Model        string
}

// ChatResult returns ready-to-use chat model plus optional refreshed auth state.
type ChatResult struct {
	Model      model.LLM
	AuthUpdate *AuthUpdate
}

// ChatOptions configures chat-model auth refresh and transport.
type ChatOptions struct {
	HTTPClient         *http.Client
	GitHubCopilotOAuth GitHubCopilotOAuthConfig
	Now                time.Time
}

// NewChatModel resolves provider auth, hides transport details, and returns ready-to-use ADK model.
func NewChatModel(ctx context.Context, req ChatRequest, opts ChatOptions) (*ChatResult, error) {
	if req.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	prof, err := Get(req.ProviderID)
	if err != nil {
		return nil, err
	}

	resolvedAuth, authUpdate, err := resolveAuthWithUpdate(ctx, req.ProviderID, req.APIKey, req.ProviderAuth, authResolveOptions{
		HTTPClient:         opts.HTTPClient,
		GitHubCopilotOAuth: opts.GitHubCopilotOAuth,
		Now:                opts.Now,
	})
	if err != nil {
		return nil, err
	}
	if prof.APIKeyRequired && resolvedAuth.APIKey == "" {
		return nil, fmt.Errorf("%s is required for provider %q", prof.RequiredCredentialName(), req.ProviderID)
	}

	return &ChatResult{
		Model:      NewOpenAIModelForProfile(req.Model, req.BaseURL, resolvedAuth.APIKey, prof, opts.HTTPClient),
		AuthUpdate: authUpdate,
	}, nil
}
