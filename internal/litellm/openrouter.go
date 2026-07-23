package litellm

import (
	"net/http"
	"strings"
)

// NewOpenRouter creates an OpenRouter adapter with tracking headers.
// It wraps the base openAICompatible with OpenRouter-specific headers and URL.
func NewOpenRouter(model, baseURL, apiKey, ref, title string, rt http.RoundTripper) LLMService {
	return &openAICompatible{
		model:    model,
		baseURL:  strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1"),
		apiKey:   apiKey,
		chatPath: "/v1/chat/completions",
		setHeaders: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+apiKey)
			r.Header.Set("HTTP-Referer", ref)
			r.Header.Set("X-Title", title)
		},
		client: makeHTTPClient(rt),
	}
}
