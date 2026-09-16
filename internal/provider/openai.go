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

// OpenAICompatible is a provider HTTP client that speaks one of two wire dialects to
// an OpenAI-compatible endpoint (OpenCode Go, the primary provider) or an Anthropic
// Messages endpoint. The dialect is chosen per model: a fixed Chat-Completions
// endpoint by default, or the Anthropic wire when the model is routed there.
type OpenAICompatible struct {
	apiKey                string
	url                   string // Chat-Completions endpoint ("" for an anthropic-only client)
	anthropicURL          string // Anthropic Messages endpoint ("" when not wired)
	http                  *http.Client
	opencodeSessionHeader bool
}

// eitriUserAgent identifies Eitri to OpenAI-compatible gateways. The OpenCode
// Go gateway requires clients to identify themselves with a specific user agent
// (never a broad/default one) so its abuse monitoring and prompt-cache tuning can
// attribute traffic.
const eitriUserAgent = "Eitri/1.0.0"

// NewOpenAICompatible returns a client for the given API key and base URL. A URL
// pointing at an Anthropic Messages endpoint (…/messages) yields an anthropic-only
// client; any other URL is treated as an OpenAI-compatible endpoint (the full
// /chat/completions path or a prefix to which it is appended).
func NewOpenAICompatible(apiKey, url string) *OpenAICompatible {
	u := strings.TrimRight(url, "/")
	if isAnthropicMessagesURL(u) {
		return &OpenAICompatible{apiKey: apiKey, anthropicURL: u}
	}
	return &OpenAICompatible{apiKey: apiKey, url: normalizeChatCompletionsURL(u)}
}

// NewOpenCodeGo returns an OpenCode Go client that identifies each conversation
// to the service and routes Anthropic-wire models (Minimax, Qwen, Union Alpha)
// to the sibling /v1/messages endpoint.
func NewOpenCodeGo(apiKey, url string) *OpenAICompatible {
	chat := normalizeChatCompletionsURL(url)
	client := NewOpenAICompatible(apiKey, chat)
	client.url = chat
	client.anthropicURL = strings.TrimSuffix(chat, "/chat/completions") + "/messages"
	client.opencodeSessionHeader = true
	return client
}

func normalizeChatCompletionsURL(raw string) string {
	u := strings.TrimRight(raw, "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

// isAnthropicMessagesURL reports whether a base URL points at an Anthropic
// Messages endpoint (the path ends in /messages, the canonical /v1/messages route).
func isAnthropicMessagesURL(u string) bool {
	return strings.HasSuffix(u, "/messages")
}

// openCodeAnthropicModel reports whether an OpenCode Go model is served over the
// Anthropic Messages wire. OpenCode Go's model-discovery response carries no
// endpoint metadata, so this static classifier mirrors the documented endpoint
// table (union/minimax/qwen run on /v1/messages; deepseek/glm/kimi/hy run on
// Chat Completions). Unknown models default to the Chat-Completions wire.
func openCodeAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "union-") ||
		strings.HasPrefix(model, "minimax-") ||
		strings.HasPrefix(model, "qwen")
}

// Models implements ModelLister: it GETs the provider's /models endpoint and returns the discovered model catalog. An anthropic-only endpoint (custom-openai Messages URL) has no OpenAI-style /models route, so discovery reports ErrNoDiscovery.
func (o *OpenAICompatible) Models(ctx context.Context) ([]ModelInfo, error) {
	if o.url == "" {
		return nil, ErrNoDiscovery
	}
	base := strings.TrimSuffix(o.url, "/chat/completions")
	modelsURL := strings.TrimSuffix(base, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	httpReq.Header.Set("User-Agent", eitriUserAgent)
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
	if o.anthropicURL != "" && (o.url == "" || o.onAnthropicWire(req.Model)) {
		return o.streamAnthropic(ctx, req)
	}
	return o.streamChat(ctx, req)
}

// onAnthropicWire reports whether req.Model should ride the Anthropic Messages
// wire: always for an anthropic-only endpoint (custom-openai Messages URL), and
// by OpenCode Go model classification otherwise.
func (o *OpenAICompatible) onAnthropicWire(model string) bool {
	if o.url == "" {
		return true
	}
	return openCodeAnthropicModel(model)
}

// streamChat sends the turn over the Chat-Completions wire to o.url.
func (o *OpenAICompatible) streamChat(ctx context.Context, req Request) (Stream, error) {
	body, err := chatDialect.Build(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	if o.opencodeSessionHeader && req.SessionKey != "" {
		httpReq.Header.Set("X-Opencode-Session", req.SessionKey)
	}
	httpReq.Header.Set("User-Agent", eitriUserAgent)

	client := resolveClient(o.http)
	resp, err := doWithRetry(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap error payloads to a sane size
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	streamBody := watchForIdle(resp.Body)
	return closeBodyOnDone(chatDialect.Stream(streamBody), streamBody), nil
}

// streamAnthropic sends the turn over the Anthropic Messages wire to o.anthropicURL,
// authenticating with x-api-key (not the Bearer header the chat wire uses).
func (o *OpenAICompatible) streamAnthropic(ctx context.Context, req Request) (Stream, error) {
	body, err := anthropicDialect.Build(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.anthropicURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", o.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	// The session header is an OpenCode Go routing optimization the gateway
	// requires when a SessionKey exists; other Anthropic endpoints ignore it.
	if req.SessionKey != "" {
		httpReq.Header.Set("X-Opencode-Session", req.SessionKey)
	}
	httpReq.Header.Set("User-Agent", eitriUserAgent)

	client := resolveClient(o.http)
	resp, err := doWithRetry(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap error payloads to a sane size
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	streamBody := watchForIdle(resp.Body)
	return closeBodyOnDone(anthropicDialect.Stream(streamBody), streamBody), nil
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
