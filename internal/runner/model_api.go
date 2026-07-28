package runner

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

func resolveModelAPI(ctx context.Context, cfg RunConfig, persistAuth PersistAuthFunc) string {
	if cfg.ProviderID != "github_copilot" {
		return ""
	}
	if modelAPI := strings.TrimSpace(cfg.ModelAPI); modelAPI != "" {
		return modelAPI
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return ""
	}
	result, err := provider.DiscoverModels(ctx, provider.DiscoveryRequest{
		ProviderID:    cfg.ProviderID,
		BaseURL:       cfg.BaseURL,
		APIKey:        cfg.APIKey,
		ProviderAuth:  cfg.ProviderAuth,
		SelectedModel: cfg.ModelName,
	}, provider.DiscoveryOptions{PersistAuth: persistAuth})
	if err != nil || result == nil {
		return ""
	}
	return result.ModelAPIs[cfg.ModelName]
}
