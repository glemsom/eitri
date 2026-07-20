package litellm

import (
	"net/http"
)

// NewGitHubCopilot creates a GitHub Copilot adapter.
// It wraps the base openAICompatible with Copilot-specific headers and URL.
func NewGitHubCopilot(model, baseURL, apiKey string) LLMService {
	return &openAICompatible{
		model:    model,
		baseURL:  baseURL,
		apiKey:   apiKey,
		chatPath: "/chat/completions",
		setHeaders: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+apiKey)
			r.Header.Set("Editor-Version", "vscode/1.80.0")
			r.Header.Set("User-Agent", "GithubCopilot/1.100.0")
		},
		client: defaultHTTPClient,
	}
}
