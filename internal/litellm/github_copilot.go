package litellm

import (
	"context"
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

func (s *GitHubCopilot) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = false

	resp, err := doChatRequest[openAIReq, openAIResp](ctx, s.client, s.baseURL+"/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
		r.Header.Set("Editor-Version", "vscode/1.80.0")
		r.Header.Set("User-Agent", "GithubCopilot/1.100.0")
	})
	if err != nil {
		return nil, err
	}

	return fromOpenAIResponse(*resp), nil
}

func (s *GitHubCopilot) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true

	resp, err := doChatStreamRequest(ctx, s.client, s.baseURL+"/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
		r.Header.Set("Editor-Version", "vscode/1.80.0")
		r.Header.Set("User-Agent", "GithubCopilot/1.100.0")
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go readOpenAIStream(ctx, resp.Body, ch)
	return ch, nil
}
