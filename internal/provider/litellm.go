// Package provider owns LLM provider profile definitions, authentication
// handling, and model discovery.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// DebugPrompt logs full LLM request payloads at Info level when true.
	DebugPrompt bool
	// DebugRequest logs full LLM request/response payloads at Info level when true.
	DebugRequest bool
	// DebugLLMDir is the directory for writing LLM debug files on error.
	// When empty, no debug files are written.
	DebugLLMDir string
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

	opts := []litellm.ClientOption{}

	if cfg.DebugPrompt || cfg.DebugRequest || cfg.DebugLLMDir != "" {
		opts = append(opts, litellm.WithHook(&DebugHook{
			DebugPrompt:  cfg.DebugPrompt,
			DebugRequest: cfg.DebugRequest,
			DebugLLMDir:  cfg.DebugLLMDir,
		}))
	}

	return litellm.New(prov, opts...)
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

// DebugHook implements litellm.Hook to provide debug logging and error dump
// files matching the behaviour of the deprecated hand-rolled adapters.
//
//   - DebugPrompt: logs the full request payload at Info level before sending
//   - DebugRequest: logs both request and response payloads at Info level
//   - DebugLLMDir: writes a JSON debug file to dir when the request fails
type DebugHook struct {
	DebugPrompt  bool
	DebugRequest bool
	DebugLLMDir  string
}

// BeforeRequest implements litellm.Hook.
func (h *DebugHook) BeforeRequest(ctx context.Context, meta litellm.CallMeta, req *litellm.Request) {
	if !h.DebugPrompt && !h.DebugRequest {
		return
	}
	body, _ := json.MarshalIndent(req, "", "  ")
	slog.Info("llm request",
		slog.String("provider", meta.Provider),
		slog.String("model", meta.Model),
		slog.String("operation", meta.Operation),
		slog.Bool("streaming", meta.Streaming),
		slog.String("body", string(body)),
	)
}

// AfterResponse implements litellm.Hook.
func (h *DebugHook) AfterResponse(ctx context.Context, meta litellm.CallMeta, resp *litellm.Response, err error) {
	if err != nil {
		// On error, write a debug file if DebugLLMDir is configured.
		if h.DebugLLMDir != "" {
			h.writeDebugFile(meta, nil, err)
		}
		return
	}

	if !h.DebugRequest {
		return
	}

	body, _ := json.MarshalIndent(resp, "", "  ")
	slog.Info("llm response",
		slog.String("provider", meta.Provider),
		slog.String("model", meta.Model),
		slog.String("operation", meta.Operation),
		slog.Duration("duration", meta.Duration),
		slog.String("body", string(body)),
	)
}

// OnStreamEvent implements litellm.Hook. No-op in current implementation;
// stream events are not individually logged to avoid excessive output.
func (h *DebugHook) OnStreamEvent(ctx context.Context, meta litellm.CallMeta, event litellm.Event) {}

// OnStreamEnd implements litellm.Hook.
func (h *DebugHook) OnStreamEnd(ctx context.Context, meta litellm.CallMeta, err error) {
	if err != nil && h.DebugLLMDir != "" {
		h.writeDebugFile(meta, nil, err)
	}
}

// OnWarning implements litellm.Hook.
func (h *DebugHook) OnWarning(ctx context.Context, meta litellm.CallMeta, warning litellm.Warning) {
	slog.Warn("llm warning",
		slog.String("provider", meta.Provider),
		slog.String("model", meta.Model),
		slog.String("message", warning.Message),
	)
}

// writeDebugFile writes a JSON debug file to DebugLLMDir capturing the
// request metadata and the error that occurred.
//
// When reqCopy is non-nil its JSON is included; when nil the serialised
// request is reconstructed from meta (which does not carry the full body).
func (h *DebugHook) writeDebugFile(meta litellm.CallMeta, reqCopy *litellm.Request, err error) {
	if h.DebugLLMDir == "" {
		return
	}
	if err := os.MkdirAll(h.DebugLLMDir, 0o755); err != nil {
		slog.Warn("cannot create LLM debug dir", slog.String("dir", h.DebugLLMDir), slog.Any("error", err))
		return
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("%s-llm-debug-%d.json", meta.Operation, timestamp)
	path := filepath.Join(h.DebugLLMDir, filename)

	type debugEntry struct {
		Provider  string          `json:"provider"`
		Model     string          `json:"model"`
		Operation string          `json:"operation"`
		Streaming bool            `json:"streaming"`
		Request   json.RawMessage `json:"request,omitempty"`
		Error     string          `json:"error,omitempty"`
	}

	entry := debugEntry{
		Provider:  meta.Provider,
		Model:     meta.Model,
		Operation: meta.Operation,
		Streaming: meta.Streaming,
		Error:     err.Error(),
	}

	if reqCopy != nil {
		if data, marshalErr := json.Marshal(reqCopy); marshalErr == nil {
			entry.Request = data
		}
	}

	data, marshalErr := json.MarshalIndent(entry, "", "  ")
	if marshalErr != nil {
		slog.Warn("failed to marshal LLM debug entry", slog.Any("error", marshalErr))
		return
	}

	if writeErr := os.WriteFile(path, data, 0o644); writeErr != nil {
		slog.Warn("failed to write LLM debug file", slog.String("path", path), slog.Any("error", writeErr))
		return
	}

	slog.Warn("LLM debug file written", slog.String("path", path), slog.String("provider", meta.Provider), slog.String("model", meta.Model))
}
