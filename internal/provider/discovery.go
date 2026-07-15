package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DiscoveryRequest describes provider-language inputs for model discovery.
type DiscoveryRequest struct {
	ProviderID   string
	BaseURL      string
	APIKey       string
	ProviderAuth json.RawMessage
}

// AuthUpdate carries refreshed provider auth state for caller persistence.
type AuthUpdate struct {
	APIKey       string
	ProviderAuth json.RawMessage
}

// DiscoveryResult returns selectable models plus optional refreshed auth state.
type DiscoveryResult struct {
	Models     []string
	AuthUpdate *AuthUpdate
}

// DiscoveryOptions configures discovery transport and refresh-aware auth behavior.
type DiscoveryOptions struct {
	HTTPClient         *http.Client
	GitHubCopilotOAuth GitHubCopilotOAuthConfig
	Now                time.Time
	PersistAuth        PersistAuthFunc // optional: called on auth refresh instead of returning AuthUpdate
}

// DiscoverModels resolves auth, refreshes provider-owned auth when needed, and
// returns selectable models plus refreshed auth state for caller persistence.
func DiscoverModels(ctx context.Context, req DiscoveryRequest, opts DiscoveryOptions) (*DiscoveryResult, error) {
	if req.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	prof, err := getProfile(req.ProviderID)
	if err != nil {
		return nil, err
	}

	resolvedAuth, authUpdate, err := resolveAuthWithUpdate(ctx, req.ProviderID, req.APIKey, req.ProviderAuth, authResolveOptions{
		HTTPClient:         opts.HTTPClient,
		GitHubCopilotOAuth: opts.GitHubCopilotOAuth,
		Now:                opts.Now,
	}, opts.PersistAuth)
	if err != nil {
		return nil, err
	}
	if prof.APIKeyRequired && resolvedAuth.APIKey == "" {
		return nil, fmt.Errorf("%s is required for provider %q", prof.RequiredCredentialName(), req.ProviderID)
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	modelsURL := prof.ModelListURL(req.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	prof.ApplyHeaders(httpReq, resolvedAuth.APIKey)

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Provider unreachable. Check base URL and network connection: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(providerValidationErrorMessage(req.ProviderID, resp.StatusCode))
	}

	modelIDs, err := prof.ParseModelList(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Model discovery failed: %v", err)
	}
	if len(modelIDs) == 0 {
		return nil, fmt.Errorf("Model discovery failed: no selectable models returned")
	}

	return &DiscoveryResult{
		Models:     modelIDs,
		AuthUpdate: authUpdate,
	}, nil
}

type authResolveOptions struct {
	HTTPClient         *http.Client
	GitHubCopilotOAuth GitHubCopilotOAuthConfig
	Now                time.Time
}

func resolveAuthWithUpdate(ctx context.Context, providerID, apiKey string, providerAuth json.RawMessage, opts authResolveOptions, persistAuth PersistAuthFunc) (ResolvedAuth, *AuthUpdate, error) {
	var update *AuthUpdate
	persist := func(apiKey string, raw json.RawMessage) error {
		if persistAuth != nil {
			return persistAuth(apiKey, raw)
		}
		update = &AuthUpdate{
			APIKey:       apiKey,
			ProviderAuth: cloneRawMessage(raw),
		}
		return nil
	}
	resolved, err := resolveAuthForRequest(ctx, providerID, apiKey, providerAuth, ResolveAuthOptions{
		HTTPClient:         opts.HTTPClient,
		GitHubCopilotOAuth: opts.GitHubCopilotOAuth,
		Now:                opts.Now,
		Persist:            persist,
	})
	if err != nil {
		return ResolvedAuth{}, nil, err
	}
	return resolved, update, nil
}

func providerValidationErrorMessage(providerID string, statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if providerID == "github_copilot" {
			return "Provider authentication failed. Token invalid, expired, missing Copilot entitlement, or org policy blocked access."
		}
		return "Provider authentication failed. Check API key or token and try again."
	case http.StatusNotFound:
		return "Model discovery failed. Check base URL; provider endpoint not found."
	default:
		return fmt.Sprintf("Model discovery failed: provider returned HTTP %d", statusCode)
	}
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	clone := make([]byte, len(raw))
	copy(clone, raw)
	return json.RawMessage(clone)
}
