package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// OpenAICompatible is a Chat-Completions HTTP client targeting any OpenAI-
// compatible endpoint (OpenCode Go primary provider, docs/research/
// opencode-endpoints.md §5). It serializes the request with model + messages
// and streams the SSE response through the provider seam.
type OpenAICompatible struct {
	apiKey string
	url    string
	http   *http.Client
}

// OpenAICompatible returns a client for the given Bearer key and base URL
// (the full /v1/chat/completions endpoint or a prefix to which it appends).
func NewOpenAICompatible(apiKey, url string) *OpenAICompatible {
	return &OpenAICompatible{apiKey: apiKey, url: url}
}

// Stream implements Provider with an HTTP Chat-Completions request.
func (o *OpenAICompatible) Stream(ctx context.Context, req Request) (Stream, error) {
	body, err := json.Marshal(chatCompletionBody{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   true,
		StreamOptions: &streamOptions{
			IncludeUsage: true, // opencode force-sets include_usage (research §4)
		},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	client := o.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: "provider returned non-2xx"}
	}
	return &openAIStream{ev: newSSE(resp.Body)}, nil
}

// chatCompletionBody is the OpenAI Chat-Completions request shape.
type chatCompletionBody struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// HTTPError reports a non-2xx provider response.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d", e.Code)
}

// openAIStream adapts parsed SSE events into the Stream seam, mapping [DONE]
// to a Done chunk and io.EOF to io.EOF.
type openAIStream struct {
	ev *sse
}

// Next implements Stream.
func (os *openAIStream) Next() (Chunk, error) {
	e, err := os.ev.Next()
	if errors.Is(err, io.EOF) {
		return Chunk{}, io.EOF
	}
	if err != nil {
		return Chunk{}, err
	}
	return parseEvent(e.data)
}
