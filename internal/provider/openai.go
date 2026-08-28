package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAICompatible is a Chat-Completions HTTP client targeting any OpenAI-compatible endpoint (OpenCode Go, the primary provider).
type OpenAICompatible struct {
	apiKey string
	url    string
	http   *http.Client
}

// NewOpenAICompatible returns a client for the given Bearer key and base URL (the full /v1/chat/completions endpoint or a prefix to which it appends).
func NewOpenAICompatible(apiKey, url string) *OpenAICompatible {
	return &OpenAICompatible{apiKey: apiKey, url: url}
}

// Models implements ModelLister: it GETs the provider's /models endpoint and returns the discovered model catalog.
func (o *OpenAICompatible) Models(ctx context.Context) ([]ModelInfo, error) {
	base := strings.TrimSuffix(o.url, "/chat/completions")
	modelsURL := strings.TrimSuffix(base, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	client := resolveClient(o.http)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Code: resp.StatusCode, Body: "provider model discovery returned non-2xx"}
	}
	var out modelList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		kind := inferEndpointKind(m)
		if kind == EndpointUnknown {
			kind = EndpointChatCompletions
		}
		models = append(models, ModelInfo{ID: m.ID, EndpointKind: kind})
	}
	return models, nil
}

// modelList is the OpenAI-standard model-discovery response shape plus optional endpoint metadata some providers may surface.
type modelList struct {
	Data []modelListEntry `json:"data"`
}

type modelListEntry struct {
	ID                 string            `json:"id"`
	Endpoints          []string          `json:"endpoints,omitempty"`
	SupportedEndpoints []string          `json:"supported_endpoints,omitempty"`
	Capabilities       modelCapabilities `json:"capabilities,omitempty"`
}

// modelCapabilities keeps discovery tolerant of providers that mix booleans with descriptive strings inside the capabilities bag.
type modelCapabilities map[string]json.RawMessage

func inferEndpointKind(m modelListEntry) EndpointKind {
	for _, endpoints := range [][]string{m.Endpoints, m.SupportedEndpoints} {
		for _, e := range endpoints {
			switch normalizeEndpoint(e) {
			case EndpointResponses:
				return EndpointResponses
			case EndpointChatCompletions:
				return EndpointChatCompletions
			}
		}
	}
	if capabilityEnabled(m.Capabilities, "responses") {
		return EndpointResponses
	}
	if capabilityEnabled(m.Capabilities, "chat_completions") || capabilityEnabled(m.Capabilities, "chat/completions") {
		return EndpointChatCompletions
	}
	return EndpointUnknown
}

func capabilityEnabled(caps modelCapabilities, key string) bool {
	raw, ok := caps[key]
	if !ok {
		return false
	}
	var enabled bool
	return json.Unmarshal(raw, &enabled) == nil && enabled
}

func normalizeEndpoint(s string) EndpointKind {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "v1/")
	s = strings.TrimPrefix(s, "openai/")
	s = strings.TrimSpace(s)
	switch s {
	case "responses":
		return EndpointResponses
	case "chat/completions", "chat_completions", "chat-completions":
		return EndpointChatCompletions
	default:
		return EndpointUnknown
	}
}

// Stream implements Provider with an HTTP Chat-Completions request shaped and parsed by the Chat-Completions dialect.
func (o *OpenAICompatible) Stream(ctx context.Context, req Request) (Stream, error) {
	body, err := chatDialect.Build(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	client := resolveClient(o.http)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap error payloads to a sane size
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	return closeBodyOnDone(chatDialect.Stream(resp.Body), resp.Body), nil
}

// SupportedGenerationControls delegates to the Chat-Completions dialect's declared capabilities.
func (o *OpenAICompatible) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return chatDialect.Capabilities(), nil
}

// HTTPError reports a non-2xx provider response.
type HTTPError struct {
	Code int
	Body string
}

// Error describes the failed provider response.
func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("provider returned HTTP %d", e.Code)
	}
	return fmt.Sprintf("provider returned HTTP %d: %s", e.Code, e.Body)
}
