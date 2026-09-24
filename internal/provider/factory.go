package provider

import (
	"errors"
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
	TraceSink   HTTPTraceSink

	CopilotRefresh RefreshFunc
	CopilotPersist func(config.CopilotConfig) error
}

// ErrMissingCredentials is returned by FromConfig when the selected provider
// has no usable credentials, so the caller can offer setup instead of aborting.
var ErrMissingCredentials = errors.New("provider credentials missing")

func FromConfig(cfg config.Config, env ProviderEnv) (Provider, error) {
	httpClient := traceClient(env.HTTP, env.TraceSink)
	switch ProviderID(cfg.Provider) {
	case ProviderOpenCodeGo:
		url := env.OpenCodeURL
		if url == "" {
			url = DefaultOpenCodeURL
		}
		key := env.OpenCodeKey
		if key == "" {
			key = cfg.OpenCodeGo.Key
		}
		if key == "" {
			return nil, fmt.Errorf("%w: opencode-go provider selected but no API key configured (set it in Settings)", ErrMissingCredentials)
		}
		p := NewOpenCodeGo(key, url)
		p.http = httpClient
		return p, nil

	case ProviderCustomOpenAI:
		if cfg.CustomOpenAI.BaseURL == "" {
			return nil, fmt.Errorf("%w: custom-openai provider selected but no base URL configured (set it in Settings)", ErrMissingCredentials)
		}
		p := NewOpenAICompatible(cfg.CustomOpenAI.Key, cfg.CustomOpenAI.BaseURL)
		p.http = httpClient
		return p, nil

	case ProviderCopilot:
		if cfg.Copilot.AccessToken == "" && cfg.Copilot.RefreshToken == "" {
			return nil, fmt.Errorf("%w: github-copilot provider selected but no credential configured (run /login in the TUI)", ErrMissingCredentials)
		}
		return NewCopilot(cfg.Copilot, DefaultCopilotURL, httpClient, env.CopilotRefresh, env.CopilotPersist), nil

	default:
		return nil, fmt.Errorf("unknown provider %q; supported: opencode-go, github-copilot, custom-openai", cfg.Provider)
	}
}

func traceClient(client *http.Client, sink HTTPTraceSink) *http.Client {
	if sink == nil {
		return client
	}
	if client == nil {
		client = http.DefaultClient
	}
	traced := *client
	traced.Transport = NewTraceTransport(client.Transport, sink)
	return &traced
}
