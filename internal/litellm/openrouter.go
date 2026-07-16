package litellm

import (
	"context"
	"net/http"
	"strings"
)

// OpenRouter implements LLMService for OpenRouter API.
// It adds OpenRouter tracking headers on top of an OpenAI-compatible request.
type OpenRouter struct {
	model   string
	baseURL string
	apiKey  string
	ref     string
	title   string
	client  *http.Client
}

// NewOpenRouter creates an OpenRouter adapter with tracking headers.
func NewOpenRouter(model, baseURL, apiKey, ref, title string) *OpenRouter {
	return &OpenRouter{
		model:   model,
		baseURL: strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1"),
		apiKey:  apiKey,
		ref:     ref,
		title:   title,
		client:  defaultHTTPClient,
	}
}

func (s *OpenRouter) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = false

	resp, err := doChatRequest[openAIReq, openAIResp](ctx, s.client, s.baseURL+"/v1/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
		r.Header.Set("HTTP-Referer", s.ref)
		r.Header.Set("X-Title", s.title)
	})
	if err != nil {
		return nil, err
	}

	return fromOpenAIResponse(*resp), nil
}

func (s *OpenRouter) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true

	resp, err := doChatStreamRequest(ctx, s.client, s.baseURL+"/v1/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
		r.Header.Set("HTTP-Referer", s.ref)
		r.Header.Set("X-Title", s.title)
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go readOpenAIStream(ctx, resp.Body, ch)
	return ch, nil
}
