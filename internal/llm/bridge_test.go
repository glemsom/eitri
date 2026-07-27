package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/provider"
)

// newBridgeService creates an LLMService backed by the litellm bridge.
func newBridgeService(cfg llm.AdapterConfig) (llm.LLMService, error) {
	client, err := provider.NewLitellmClient(provider.LitellmConfig{
		ProviderID:      cfg.ProviderID,
		Model:           cfg.Model,
		BaseURL:         cfg.BaseURL,
		APIKey:          cfg.APIKey,
		OpenRouterRef:   cfg.OpenRouterRef,
		OpenRouterTitle: cfg.OpenRouterTitle,
		RoundTripper:    cfg.RoundTripper,
	})
	if err != nil {
		return nil, err
	}
	return llm.NewBridge(client), nil
}

// ————— helper —————

func collectStreamEvents(ctx context.Context, stream <-chan llm.StreamEvent) ([]llm.StreamEvent, error) {
	var events []llm.StreamEvent
	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return events, nil
			}
			if evt.Error != nil {
				return events, evt.Error
			}
			events = append(events, evt)
		case <-ctx.Done():
			return events, ctx.Err()
		}
	}
}

// ————— Factory routing tests —————

func TestBridge_OpenCodeGoOpenAIRoute(t *testing.T) {
	t.Parallel()
	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewLitellmClient returned nil")
	}

	// Should route to OpenAI adapter — verify by making a request to a test server
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`)
	}))
	defer chatSrv.Close()

	svc, err = newBridgeService(llm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("Content = %q, want %q", resp.Content, "hello")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 30 {
		t.Fatalf("Usage = %+v, want total=30", resp.Usage)
	}
}

func TestBridge_OpenCodeGoAnthropicRoute(t *testing.T) {
	t.Parallel()
	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "qwen2.5-72b",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewLitellmClient returned nil")
	}

	// Should route to Anthropic adapter — test with a mock server
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Fatalf("x-api-key = %q, want sk-test", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatal("anthropic-version header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello from qwen"}],"model":"qwen2.5-72b","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer chatSrv.Close()

	svc, err = newBridgeService(llm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "qwen2.5-72b",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), llm.Request{
		Model:    "qwen2.5-72b",
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "hello from qwen" {
		t.Fatalf("Content = %q, want %q", resp.Content, "hello from qwen")
	}
}

func TestBridge_OpenRouterRoute(t *testing.T) {
	t.Parallel()
	var gotReferer, gotTitle string
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"from openrouter"},"index":0,"finish_reason":"stop"}]}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID:      "openrouter",
		Model:           "anthropic/claude-3",
		BaseURL:         chatSrv.URL,
		APIKey:          "sk-or-test",
		OpenRouterRef:   "https://eitri.ai",
		OpenRouterTitle: "Eitri",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "anthropic/claude-3",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if gotReferer != "https://eitri.ai" {
		t.Fatalf("HTTP-Referer = %q, want https://eitri.ai", gotReferer)
	}
	if gotTitle != "Eitri" {
		t.Fatalf("X-Title = %q, want Eitri", gotTitle)
	}
}

func TestBridge_GitHubCopilotRoute(t *testing.T) {
	t.Parallel()
	var gotAuth, gotEditorVer, gotUserAgent string
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEditorVer = r.Header.Get("Editor-Version")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"copilot reply"},"index":0,"finish_reason":"stop"}]}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "github_copilot",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "gho-token",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if gotAuth != "Bearer gho-token" {
		t.Fatalf("Authorization = %q, want Bearer gho-token", gotAuth)
	}
	if gotEditorVer != "vscode/1.80.0" {
		t.Fatalf("Editor-Version = %q, want vscode/1.80.0", gotEditorVer)
	}
	if gotUserAgent != "GithubCopilot/1.100.0" {
		t.Fatalf("User-Agent = %q, want GithubCopilot/1.100.0", gotUserAgent)
	}
}

func TestBridge_CustomOpenAI(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer custom-key" {
			t.Fatalf("Authorization = %q, want Bearer custom-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"custom response"},"index":0,"finish_reason":"stop"}]}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "custom-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "custom response" {
		t.Fatalf("Content = %q, want %q", resp.Content, "custom response")
	}
}

// ————— Chat response parsing —————

func TestBridge_SimpleTextResponse(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, world!"},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "Hello, world!" {
		t.Fatalf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 8 {
		t.Fatalf("Usage.Total = %d, want 8", resp.Usage.TotalTokens)
	}
}

func TestBridge_ToolCallResponse(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"",
					"tool_calls":[
						{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}},
						{"id":"call_2","type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"CET\"}"}}
					]
				},
				"index":0,
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}
		}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("ToolCalls count = %d, want 2", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Function.Name = %q, want %q", resp.ToolCalls[0].Function.Name, "get_weather")
	}
	if resp.ToolCalls[1].Function.Name != "get_time" {
		t.Fatalf("ToolCalls[1].Function.Name = %q, want %q", resp.ToolCalls[1].Function.Name, "get_time")
	}
}

// ————— Streaming —————

func TestBridge_StreamTextDeltas(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("not a flusher")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0,\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"index\":0,\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2", len(events))
	}

	// Check first token
	if events[0].Type != llm.StreamEventTypeToken {
		t.Fatalf("events[0].Type = %d, want Token", events[0].Type)
	}
	if events[0].Content != "Hello" {
		t.Fatalf("events[0].Content = %q, want %q", events[0].Content, "Hello")
	}

	// Check second token
	if events[1].Type != llm.StreamEventTypeToken {
		t.Fatalf("events[1].Type = %d, want Token", events[1].Type)
	}
	if events[1].Content != " world" {
		t.Fatalf("events[1].Content = %q, want %q", events[1].Content, " world")
	}

	// Check done event
	last := events[len(events)-1]
	if last.Type != llm.StreamEventTypeDone {
		t.Fatalf("last event.Type = %d, want Done", last.Type)
	}
	if last.Usage == nil || last.Usage.TotalTokens != 4 {
		t.Fatalf("last.Usage.Total = %d, want 4", last.Usage.TotalTokens)
	}
}

func TestBridge_StreamToolCallDeltas(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("not a flusher")
		}
		// OpenAI-style streaming tool calls with delta accumulation
		fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"index":0,"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]},"index":0,"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"index":0,"finish_reason":null}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"index":0,"finish_reason":"tool_calls"}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collect error: %v", err)
	}

	// Should have one ToolCall event and one Done event
	var toolCalls int
	for _, e := range events {
		if e.Type == llm.StreamEventTypeToolCall {
			toolCalls++
			if len(e.ToolCalls) != 1 {
				t.Fatalf("ToolCall event has %d calls, want 1", len(e.ToolCalls))
			}
			if e.ToolCalls[0].Function.Name != "get_weather" {
				t.Fatalf("ToolCall name = %q, want %q", e.ToolCalls[0].Function.Name, "get_weather")
			}
			// Check the accumulated arguments
			var args map[string]string
			if err := json.Unmarshal([]byte(e.ToolCalls[0].Function.Arguments), &args); err != nil {
				t.Fatalf("unmarshal args: %v", err)
			}
			if args["location"] != "Paris" {
				t.Fatalf("args.location = %q, want %q", args["location"], "Paris")
			}
		}
	}
	if toolCalls != 1 {
		t.Fatalf("got %d ToolCall events, want 1", toolCalls)
	}
	if events[len(events)-1].Type != llm.StreamEventTypeDone {
		t.Fatalf("last event type = %d, want Done", events[len(events)-1].Type)
	}
}

func TestBridge_UnsupportedProvider(t *testing.T) {
	t.Parallel()
	_, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "nonexistent",
		Model:      "gpt-4.1",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("error = %q, want containing 'unsupported provider'", err.Error())
	}
}

func TestBridge_Error401(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"auth_error","code":"401"}}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "bad-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Authentication failed") && !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %q, want auth-related error", err.Error())
	}
}

func TestBridge_Error429(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"429"}}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Rate limited") && !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %q, want rate-limit error", err.Error())
	}
}

func TestBridge_Error5xx(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"Internal server error","type":"server_error","code":"500"}}`)
	}))
	defer chatSrv.Close()

	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBridge_NetworkError(t *testing.T) {
	t.Parallel()
	// Use an unreachable address
	svc, err := newBridgeService(llm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "gpt-4.1",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("NewLitellmClient error: %v", err)
	}

	_, err = svc.Chat(context.Background(), llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
