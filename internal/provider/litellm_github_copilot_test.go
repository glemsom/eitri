package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGitHubCopilotClientUsesRootChatCompletionsPath(t *testing.T) {
	t.Parallel()

	client, err := NewLitellmClient(LitellmConfig{
		ProviderID: "github_copilot",
		Model:      "gpt-4.1",
		BaseURL:    "https://api.githubcopilot.com",
		APIKey:     "gho-test",
		RoundTripper: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/chat/completions" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("404 page not found")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"model":"gpt-4.1",
					"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]
				}`)),
				Header:  make(http.Header),
				Request: req,
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := client.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{litellm.UserText("Respond with hi")},
	})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	if got := resp.Text(); got != "hi" {
		t.Fatalf("response text = %q, want %q", got, "hi")
	}
}

func TestGitHubCopilotClientUsesRootResponsesPath(t *testing.T) {
	t.Parallel()

	client, err := NewLitellmClient(LitellmConfig{
		ProviderID: "github_copilot",
		Model:      "gpt-5.5",
		ModelAPI:   GitHubCopilotAPIResponses,
		BaseURL:    "https://api.githubcopilot.com",
		APIKey:     "gho-test",
		RoundTripper: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/responses" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("404 page not found")),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"id":"resp_123",
					"model":"gpt-5.5",
					"status":"completed",
					"output_text":"hi",
					"output":[],
					"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
				}`)),
				Header:  make(http.Header),
				Request: req,
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := client.Chat(context.Background(), litellm.Request{
		Model:    "gpt-5.5",
		Messages: []litellm.Message{litellm.UserText("Respond with hi")},
	})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	if got := resp.Text(); got != "hi" {
		t.Fatalf("response text = %q, want %q", got, "hi")
	}
}

func TestGitHubCopilotClientSetsCopilotHeaders(t *testing.T) {
	t.Parallel()

	client, err := NewLitellmClient(LitellmConfig{
		ProviderID: "github_copilot",
		Model:      "gpt-4.1",
		BaseURL:    "https://api.githubcopilot.com",
		APIKey:     "gho-test",
		RoundTripper: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			missing := []string{}
			if got := req.Header.Get("User-Agent"); got != "GithubCopilot/1.100.0" {
				missing = append(missing, fmt.Sprintf("User-Agent=%q", got))
			}
			if got := req.Header.Get("Editor-Version"); got == "" {
				missing = append(missing, "Editor-Version missing")
			}
			if got := req.Header.Get("Openai-Intent"); got != "conversation-panel" {
				missing = append(missing, fmt.Sprintf("Openai-Intent=%q", got))
			}
			if got := req.Header.Get("X-GitHub-Api-Version"); got != "2026-06-01" {
				missing = append(missing, fmt.Sprintf("X-GitHub-Api-Version=%q", got))
			}
			if got := req.Header.Get("x-initiator"); got != "user" {
				missing = append(missing, fmt.Sprintf("x-initiator=%q", got))
			}
			if len(missing) > 0 {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(strings.Join(missing, ", "))),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"model":"gpt-4.1",
					"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]
				}`)),
				Header:  make(http.Header),
				Request: req,
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = client.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{litellm.UserText("Respond with hi")},
	})
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
}
