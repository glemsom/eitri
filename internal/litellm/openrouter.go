package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
		baseURL: baseURL,
		apiKey:  apiKey,
		ref:     ref,
		title:   title,
		client:  defaultHTTPClient,
	}
}

func (s *OpenRouter) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = false
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("HTTP-Referer", s.ref)
	httpReq.Header.Set("X-Title", s.title)

	resp, err := doRequest(ctx, s.client, httpReq)
	if err != nil {
		return nil, err
	}

	respBody, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, classifyHTTPError(resp.StatusCode, respBody)
	}

	var openAIResp openAIResp
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return fromOpenAIResponse(openAIResp), nil
}

func (s *OpenRouter) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("HTTP-Referer", s.ref)
	httpReq.Header.Set("X-Title", s.title)

	resp, err := doRequest(ctx, s.client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		return nil, classifyHTTPError(resp.StatusCode, body)
	}

	ch := make(chan StreamEvent, 64)
	go readOpenAIStream(ctx, resp.Body, ch)
	return ch, nil
}
