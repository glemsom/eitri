package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// GitHubCopilot implements LLMService for GitHub Copilot API.
// It wraps the OpenAI-compatible adapter with Copilot-specific headers.
type GitHubCopilot struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewGitHubCopilot creates a GitHub Copilot adapter.
func NewGitHubCopilot(model, baseURL, apiKey string) *GitHubCopilot {
	return &GitHubCopilot{
		model:   model,
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  defaultHTTPClient,
	}
}

func (s *GitHubCopilot) copilotHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	// Copilot API expects headers matching the official VSCode extension.
	req.Header.Set("Editor-Version", "vscode/1.80.0")
	req.Header.Set("User-Agent", "GithubCopilot/1.100.0")
}

func (s *GitHubCopilot) Chat(ctx context.Context, req Request) (*Response, error) {
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
	s.copilotHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")

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

func (s *GitHubCopilot) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
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
	s.copilotHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

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
