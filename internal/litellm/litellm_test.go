package litellm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/litellm"
)

// ————— helper —————

func collectStreamEvents(ctx context.Context, stream <-chan litellm.StreamEvent) ([]litellm.StreamEvent, error) {
	var events []litellm.StreamEvent
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

func TestNewLLMService_OpenCodeGoOpenAIRoute(t *testing.T) {
	t.Parallel()
	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewLLMService returned nil")
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

	svc, err = litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hello"}},
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

func TestNewLLMService_OpenCodeGoAnthropicRoute(t *testing.T) {
	t.Parallel()
	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "qwen2.5-72b",
		BaseURL:    "https://opencode.ai/zen/go/v1",
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewLLMService returned nil")
	}

	// Should route to Anthropic adapter — test with a mock server
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path = %q, want /messages", r.URL.Path)
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

	svc, err = litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "qwen2.5-72b",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), litellm.Request{
		Model:    "qwen2.5-72b",
		Messages: []litellm.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "hello from qwen" {
		t.Fatalf("Content = %q, want %q", resp.Content, "hello from qwen")
	}
}

func TestNewLLMService_OpenRouterRoute(t *testing.T) {
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

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID:    "openrouter",
		Model:         "anthropic/claude-3",
		BaseURL:       chatSrv.URL,
		APIKey:        "sk-or-test",
		OpenRouterRef: "https://eitri.ai",
		OpenRouterTitle: "Eitri",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "anthropic/claude-3",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
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

func TestNewLLMService_GitHubCopilotRoute(t *testing.T) {
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

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "github_copilot",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "gho-token",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
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

func TestNewLLMService_CustomOpenAI(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"custom"},"index":0,"finish_reason":"stop"}]}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "custom_openai",
		Model:      "my-model",
		BaseURL:    chatSrv.URL,
		APIKey:     "",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), litellm.Request{
		Model:    "my-model",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "custom" {
		t.Fatalf("Content = %q, want %q", resp.Content, "custom")
	}
}

// ————— Non-streaming Chat —————

func TestChat_SimpleTextResponse(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello! How can I help you?"},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Fatalf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 13 {
		t.Fatalf("Usage = %+v, want total=13", resp.Usage)
	}
}

func TestChat_ToolCallResponse(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]},"index":0,"finish_reason":"tool_calls"}],"usage":{"total_tokens":50}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	resp, err := svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "weather in Paris?"}},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want 1 tool call", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Fatalf("ToolCall name = %q, want %q", tc.Function.Name, "get_weather")
	}
	if !strings.Contains(tc.Function.Arguments, "Paris") {
		t.Fatalf("ToolCall arguments = %q, want Paris", tc.Function.Arguments)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}
}

// ————— Streaming ChatStream —————

func TestChatStream_TextDeltas(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"role":"assistant","content":"Hello"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":" world"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collectStreamEvents error: %v", err)
	}

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == litellm.StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello world" {
		t.Fatalf("streamed text = %q, want %q", gotText.String(), "Hello world")
	}
}

func TestChatStream_ToolCallDeltas_NonSequentialIndices(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Chunk 1: delta for index 5 (arrives first, key >> len of eventual map)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":5,"id":"call_5","type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"UTC\"}"}}]},"index":0}]}`, "\n\n")
		// Chunk 2: delta for index 0 (arrives second)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]},"index":0}]}`, "\n\n")
		// Chunk 3: arguments for index 5
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":5,"function":{"arguments":"}"}}]},"index":0}]}`, "\n\n")
		// Chunk 4: delta for index 2 (gap: no index 1)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_2","type":"function","function":{"name":"search_docs","arguments":"{\"query\":\"api\"}"}}]},"index":0}]}`, "\n\n")
		// Finish
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"tool_calls","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "weather and time?"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collectStreamEvents error: %v", err)
	}

	// Collect names from the final tool call event
	var names []string
	for _, evt := range events {
		if evt.Type == litellm.StreamEventTypeToolCall && len(evt.ToolCalls) > 0 {
			names = nil // reset — only care about last emission
			for _, tc := range evt.ToolCalls {
				names = append(names, tc.Function.Name)
			}
		}
	}

	// All three tool calls must be present — the current buggy code
	// loses index 5 (key >= len=3) and index 2 (key >= len=3 after index 5 removal)
	// but actually with sequential loop i=0..2, it misses indices 5 and 2.
	// Actually let's think: after all chunks: map has keys [0,2,5], len=3.
	// Loop i=0→found(i=0), i=1→not found, i=2→found(i=2), exit (i<3).
	// Key 5 is skipped. So we get 2 tool calls instead of 3.
	if len(names) != 3 {
		t.Fatalf("got %d tool calls %v, want 3 tool calls", len(names), names)
	}
	// All three tool calls must be present regardless of arrival order
	got := make(map[string]bool)
	for _, n := range names {
		got[n] = true
	}
	if !got["get_weather"] || !got["get_time"] || !got["search_docs"] {
		t.Fatalf("tool call names = %v, missing some of [get_weather, get_time, search_docs]", names)
	}
}

func TestChatStream_ToolCallDeltas(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// First chunk starts a tool call
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","index":0,"function":{"name":"get_weather","arguments":""}}]},"index":0}]}`, "\n\n")
		// Second chunk appends arguments
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":\"Paris\"}"}}]},"index":0}]}`, "\n\n")
		// Finish
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"tool_calls","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collectStreamEvents error: %v", err)
	}

	// Check the final (last) tool call event has accumulated arguments
	var finalToolCall *litellm.ToolCall
	for _, evt := range events {
		if evt.Type == litellm.StreamEventTypeToolCall && len(evt.ToolCalls) > 0 {
			tc := evt.ToolCalls[0]
			finalToolCall = &tc
		}
	}
	if finalToolCall == nil {
		t.Fatal("expected tool_call stream event, got none")
	}
	if finalToolCall.Function.Name != "get_weather" {
		t.Fatalf("tool call name = %q, want %q", finalToolCall.Function.Name, "get_weather")
	}
	if !strings.Contains(finalToolCall.Function.Arguments, "Paris") {
		t.Fatalf("tool call arguments = %q, want Paris", finalToolCall.Function.Arguments)
	}
}

// ————— Error classification —————

func TestChat_Error401(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key","type":"authentication_error","code":"401"}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-bad",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "auth") {
		t.Fatalf("error = %q, want auth-related message", err.Error())
	}
}

func TestChat_Error429(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"429"}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rate limit") && !strings.Contains(strings.ToLower(err.Error()), "429") {
		t.Fatalf("error = %q, want rate limit message", err.Error())
	}
}

func TestChat_Error400ContextLength(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 8192 tokens","type":"invalid_request_error","code":"context_length_exceeded"}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context") {
		t.Fatalf("error = %q, want context-related message", err.Error())
	}
}

func TestChat_Error5xx(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"Internal server error","type":"server_error","code":"500"}}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "server") {
		t.Fatalf("error = %q, want server-related message", err.Error())
	}
}

// ————— Anthropic streaming —————

func TestChatStream_AnthropicTextDeltas(t *testing.T) {
	t.Parallel()
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"qwen2.5-72b","usage":{"input_tokens":10,"output_tokens":0}}}`, "\n\n")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, "\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello from "}}`, "\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Anthropic"}}`, "\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":10}}`, "\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`, "\n\n")
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "qwen2.5-72b",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	stream, err := svc.ChatStream(context.Background(), litellm.Request{
		Model:    "qwen2.5-72b",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	events, err := collectStreamEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("collectStreamEvents error: %v", err)
	}

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == litellm.StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello from Anthropic" {
		t.Fatalf("streamed text = %q, want %q", gotText.String(), "Hello from Anthropic")
	}
}

// ————— Unsupported provider —————

func TestNewLLMService_UnsupportedProvider(t *testing.T) {
	t.Parallel()
	_, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "nonexistent",
		Model:      "gpt-4.1",
		BaseURL:    "http://localhost",
		APIKey:     "sk-test",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
}

// ————— Messages serialization —————

func TestChat_MessagesIncludeSystemPrompt(t *testing.T) {
	t.Parallel()
	var gotMessages []litellm.Message
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []litellm.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotMessages = body.Messages
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"index":0,"finish_reason":"stop"}]}`)
	}))
	defer chatSrv.Close()

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if len(gotMessages) != 2 {
		t.Fatalf("got %d messages, want 2", len(gotMessages))
	}
	if gotMessages[0].Role != "system" || gotMessages[0].Content != "You are helpful" {
		t.Fatalf("first message = %+v, want system/You are helpful", gotMessages[0])
	}
	if gotMessages[1].Role != "user" || gotMessages[1].Content != "hi" {
		t.Fatalf("second message = %+v, want user/hi", gotMessages[1])
	}
}

// ————— Network error —————

func TestChat_NetworkError(t *testing.T) {
	t.Parallel()
	// Server that closes connection immediately
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	chatSrv.Close() // Close so connection fails

	svc, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: "opencode_go",
		Model:      "gpt-4.1",
		BaseURL:    chatSrv.URL,
		APIKey:     "sk-test",
	})
	if err != nil {
		t.Fatalf("NewLLMService error: %v", err)
	}

	_, err = svc.Chat(context.Background(), litellm.Request{
		Model:    "gpt-4.1",
		Messages: []litellm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for closed server, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unreachable") && !strings.Contains(strings.ToLower(err.Error()), "refused") && !strings.Contains(strings.ToLower(err.Error()), "closed") && !strings.Contains(strings.ToLower(err.Error()), "connection") {
		t.Fatalf("error = %q, want network-related message", err.Error())
	}
}
