package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/agent"
	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func collectErrors(seq func(func(*model.LLMResponse, error) bool)) (responses []*model.LLMResponse, lastErr error) {
	for resp, err := range seq {
		if err != nil {
			lastErr = err
			continue
		}
		if resp != nil {
			responses = append(responses, resp)
		}
	}
	return
}

func fakeLLMServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		switch mode {
		case "unauthorized":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"auth_error","code":"unauthorized"}}`)
		case "ratelimit":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limited"}}`)
		case "context-length":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"maximum context length 4096 tokens exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`)
		case "stream-tool-calls":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(200)
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"I'll check.","tool_calls":null},"finish_reason":null}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"terminal_execute","arguments":"{\"command\":\"echo hello\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "stream-multi-tool":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(200)
			// Fragment 1: name + partial args: {"command":"
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"terminal_execute","arguments":"{\"command\":\""}}]},"finish_reason":null}]}`, "\n\n")
			// Fragment 2: rest of args: echo hello"}
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"echo hello\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default: // "ok"
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(200)
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"Hello!"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":" How can I help?"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18}}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOpenAIModel_Name(t *testing.T) {
	m := agent.NewOpenAIModel("gpt-4", "http://example.com", "sk-test")
	if m.Name() != "gpt-4" {
		t.Errorf("Name() = %q, want %q", m.Name(), "gpt-4")
	}
}

func TestOpenAIModel_StreamingText(t *testing.T) {
	srv := fakeLLMServer(t, "ok")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	req := &model.LLMRequest{
		Model: "gpt-4",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}
	resp, err := collectErrors(m.GenerateContent(context.Background(), req, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp) < 2 {
		t.Fatalf("expected >=2 responses, got %d", len(resp))
	}
	if !resp[0].Partial {
		t.Errorf("first resp should be Partial")
	}
	if resp[0].Content == nil || len(resp[0].Content.Parts) == 0 || resp[0].Content.Parts[0].Text != "Hello!" {
		t.Errorf("first token text unexpected")
	}
	last := resp[len(resp)-1]
	if !last.TurnComplete {
		t.Errorf("last should be TurnComplete")
	}
	if last.UsageMetadata == nil || last.UsageMetadata.TotalTokenCount != 18 {
		t.Errorf("usage metadata missing/wrong")
	}
}

func TestOpenAIModel_ExistingProvidersUseProfileChatCompletionsPath(t *testing.T) {
	for _, providerID := range []string{"opencode_go", "custom_openai"} {
		t.Run(providerID, func(t *testing.T) {
			srv := fakeLLMServer(t, "ok")
			m, err := agent.NewOpenAIModelForProvider("gpt-4", srv.URL+"/v1", "sk-test", providerID)
			if err != nil {
				t.Fatalf("NewOpenAIModelForProvider error: %v", err)
			}
			m.MaxRetries = 0
			req := &model.LLMRequest{
				Model:    "gpt-4",
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
			}
			_, err = collectErrors(m.GenerateContent(context.Background(), req, true))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOpenAIModel_GitHubCopilotStreamingTextUsesCopilotTransport(t *testing.T) {
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		assertCopilotHeaders(t, r)
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Model != "gpt-4.1" {
			t.Errorf("model = %q, want gpt-4.1", body.Model)
		}
		if !body.Stream {
			t.Errorf("stream = false, want true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"Copilot says hi"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	m, err := agent.NewOpenAIModelForProvider("gpt-4.1", srv.URL, "gh-token", "github_copilot")
	if err != nil {
		t.Fatalf("NewOpenAIModelForProvider error: %v", err)
	}
	m.MaxRetries = 0
	resp, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model:    "gpt-4.1",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
	if len(resp) == 0 || resp[0].Content.Parts[0].Text != "Copilot says hi" {
		t.Fatalf("first response = %+v, want Copilot says hi", resp)
	}
	if !resp[len(resp)-1].TurnComplete {
		t.Fatalf("last response should be TurnComplete")
	}
}

func TestOpenAIModel_GitHubCopilotStreamingToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		assertCopilotHeaders(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"terminal_execute","arguments":"{\"command\":\""}}]},"finish_reason":null}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"echo copilot\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	m, err := agent.NewOpenAIModelForProvider("gpt-4.1", srv.URL, "gh-token", "github_copilot")
	if err != nil {
		t.Fatalf("NewOpenAIModelForProvider error: %v", err)
	}
	m.MaxRetries = 0
	resp, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model:    "gpt-4.1",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Run command"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := resp[len(resp)-1]
	for _, part := range last.Content.Parts {
		if part.FunctionCall != nil && part.FunctionCall.Name == "terminal_execute" {
			if cmd, ok := part.FunctionCall.Args["command"].(string); !ok || cmd != "echo copilot" {
				t.Fatalf("command = %q, want echo copilot", cmd)
			}
			return
		}
	}
	t.Fatalf("terminal_execute tool call missing: %+v", last.Content.Parts)
}

func TestOpenAIModel_MalformedStreamingProducesUnsupportedProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprint(w, `{"message":"not an SSE stream"}`)
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model:    "gpt-4",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "streaming tool calls") {
		t.Fatalf("error = %v, want streaming tool calls unsupported error", err)
	}
}

func assertCopilotHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	want := map[string]string{
		"Authorization":        "Bearer gh-token",
		"User-Agent":           "Eitri",
		"X-GitHub-Api-Version": "2022-11-28",
		"Openai-Intent":        "conversation-panel",
		"x-initiator":          "user",
		"Accept":               "text/event-stream",
	}
	for name, value := range want {
		if got := r.Header.Get(name); got != value {
			t.Errorf("header %s = %q, want %q", name, got, value)
		}
	}
}

func TestOpenAIModel_Unauthorized(t *testing.T) {
	srv := fakeLLMServer(t, "unauthorized")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "bad-key")
	m.MaxRetries = 0
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestOpenAIModel_RateLimited(t *testing.T) {
	srv := fakeLLMServer(t, "ratelimit")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429, got: %v", err)
	}
}

func TestOpenAIModel_StreamingToolCalls(t *testing.T) {
	srv := fakeLLMServer(t, "stream-tool-calls")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	req := &model.LLMRequest{
		Model: "gpt-4",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Run echo hello"}}},
		},
	}
	resp, err := collectErrors(m.GenerateContent(context.Background(), req, true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(resp) < 2 {
		t.Fatalf("expected >=2 responses, got %d", len(resp))
	}
	last := resp[len(resp)-1]
	if !last.TurnComplete {
		t.Errorf("should be TurnComplete")
	}
	hasTool := false
	for _, part := range last.Content.Parts {
		if part.FunctionCall != nil && part.FunctionCall.Name == "terminal_execute" {
			hasTool = true
			if cmd, ok := part.FunctionCall.Args["command"].(string); !ok || cmd != "echo hello" {
				t.Errorf("command arg = %q, want 'echo hello'", cmd)
			}
		}
	}
	if !hasTool {
		t.Errorf("expected terminal_execute tool call")
	}
}

func TestOpenAIModel_FragmentedToolCalls(t *testing.T) {
	srv := fakeLLMServer(t, "stream-multi-tool")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	req := &model.LLMRequest{
		Model: "gpt-4",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Run command"}}},
		},
	}
	resp, err := collectErrors(m.GenerateContent(context.Background(), req, true))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("no responses")
	}
	last := resp[len(resp)-1]
	hasTool := false
	for _, part := range last.Content.Parts {
		if part.FunctionCall != nil && part.FunctionCall.Name == "terminal_execute" {
			hasTool = true
			if cmd, ok := part.FunctionCall.Args["command"].(string); !ok || cmd != "echo hello" {
				t.Errorf("fragmented command = %q, want 'echo hello'", cmd)
			}
		}
	}
	if !hasTool {
		t.Errorf("expected tool call after fragment assembly")
	}
}

func TestOpenAIModel_ContextLengthExceeded(t *testing.T) {
	srv := fakeLLMServer(t, "context-length")
	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	m.MaxRetries = 0
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context") {
		t.Errorf("error should mention context, got: %v", err)
	}
}

func TestOpenAIModel_ConnectionRefused(t *testing.T) {
	m := agent.NewOpenAIModel("gpt-4", "http://127.0.0.1:19876", "sk-test")
	m.MaxRetries = 0
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should mention connection refused, got: %v", err)
	}
}

func TestOpenAIModel_RetryOnRateLimit(t *testing.T) {
	t.Parallel()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call: rate limit
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`)
			return
		}
		// Second call: success
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"Hello after retry!"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	resp, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", callCount)
	}
	if len(resp) < 1 {
		t.Fatalf("expected at least 1 response, got %d", len(resp))
	}
	last := resp[len(resp)-1]
	if last.Content == nil || len(last.Content.Parts) == 0 || last.Content.Parts[0].Text != "Hello after retry!" {
		t.Errorf("expected 'Hello after retry!', got %+v", last.Content)
	}
}

func TestOpenAIModel_RetryOnServerError(t *testing.T) {
	t.Parallel()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			// First two calls: 503
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"Service unavailable","type":"server_error"}}`)
			return
		}
		// Third call: success
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"Success!"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	resp, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", callCount)
	}
	if len(resp) < 1 {
		t.Fatalf("expected at least 1 response, got %d", len(resp))
	}
}

func TestOpenAIModel_RetriesExhausted(t *testing.T) {
	t.Parallel()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"message":"Service unavailable","type":"server_error"}}`)
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if !strings.Contains(err.Error(), "after 3 retries") {
		t.Errorf("error should mention retries exhausted, got: %v", err)
	}
	// Expect 4 calls: original + 3 retries
	if callCount != 4 {
		t.Errorf("expected 4 calls (original + 3 retries), got %d", callCount)
	}
}

func TestOpenAIModel_NoRetryOnAuthError(t *testing.T) {
	t.Parallel()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"auth_error"}}`)
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "bad-key")
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	// Non-retryable: should only be 1 call
	if callCount != 1 {
		t.Errorf("expected 1 call (no retry on auth), got %d", callCount)
	}
}

func TestOpenAIModel_NoRetryOnContextLength(t *testing.T) {
	t.Parallel()
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"maximum context length 4096 tokens exceeded","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(srv.Close)

	m := agent.NewOpenAIModel("gpt-4", srv.URL, "sk-test")
	_, err := collectErrors(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (no retry on context length), got %d", callCount)
	}
}
