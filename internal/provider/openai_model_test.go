package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestPromptCacheKey_AbsentByDefault(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	// github_copilot profile has supportsPromptCache: false
	prof, err := getProfile("github_copilot")
	if err != nil {
		t.Fatalf("getProfile error: %v", err)
	}
	if prof.supportsPromptCache {
		t.Fatal("github_copilot.supportsPromptCache must be false for this test")
	}
	m := newOpenAIModelForProfile("gpt-4", srv.URL, "sk-test", prof, nil)
	m.MaxRetries = 0
	m.SessionID = "session-123"

	_, err = collectTestResponses(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody == nil {
		t.Fatal("server did not receive request body")
	}
	if v, ok := gotBody["prompt_cache_key"]; ok && v != "" {
		t.Errorf("prompt_cache_key = %q, want absent or empty (flag disabled)", v)
	}
}

func TestPromptCacheKey_PresentWhenFlagAndSessionIDSet(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	// Create model with a profile clone that has supportsPromptCache: true
	prof, err := getProfile("opencode_go")
	if err != nil {
		t.Fatalf("getProfile error: %v", err)
	}
	prof.supportsPromptCache = true

	m := newOpenAIModelForProfile("gpt-4", srv.URL, "sk-test", prof, nil)
	m.MaxRetries = 0
	m.SessionID = "session-abc-123"

	_, err = collectTestResponses(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody == nil {
		t.Fatal("server did not receive request body")
	}
	v, ok := gotBody["prompt_cache_key"]
	if !ok {
		t.Fatal("prompt_cache_key missing from request body, want present")
	}
	if v != "session-abc-123" {
		t.Errorf("prompt_cache_key = %q, want %q", v, "session-abc-123")
	}
}

func TestPromptCacheKey_EmptySessionIDWhenFlagSet(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	prof, err := getProfile("opencode_go")
	if err != nil {
		t.Fatalf("getProfile error: %v", err)
	}
	prof.supportsPromptCache = true

	m := newOpenAIModelForProfile("gpt-4", srv.URL, "sk-test", prof, nil)
	m.MaxRetries = 0
	// SessionID is empty

	_, err = collectTestResponses(m.GenerateContent(context.Background(), &model.LLMRequest{
		Model: "gpt-4", Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody == nil {
		t.Fatal("server did not receive request body")
	}
	if v, ok := gotBody["prompt_cache_key"]; ok && v != "" {
		t.Errorf("prompt_cache_key = %q, want absent or empty (session ID empty)", v)
	}
}

func TestPromptCacheKey_JSONSerializeAbsentWhenFlagFalse(t *testing.T) {
	req := openAIReq{
		Model:    "gpt-4",
		Messages: []openAIMsg{{Role: "user", Content: "Hello"}},
		Stream:   true,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if _, ok := m["prompt_cache_key"]; ok {
		t.Errorf("prompt_cache_key present in JSON when zero value")
	}
}

func TestPromptCacheKey_JSONSerializePresentWhenSet(t *testing.T) {
	req := openAIReq{
		Model:          "gpt-4",
		Messages:       []openAIMsg{{Role: "user", Content: "Hello"}},
		Stream:         true,
		PromptCacheKey: "session-xyz",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	v, ok := m["prompt_cache_key"]
	if !ok {
		t.Fatal("prompt_cache_key missing from JSON")
	}
	if v != "session-xyz" {
		t.Errorf("prompt_cache_key = %q, want %q", v, "session-xyz")
	}
}

func collectTestResponses(seq func(func(*model.LLMResponse, error) bool)) ([]*model.LLMResponse, error) {
	var lastErr error
	var responses []*model.LLMResponse
	for resp, err := range seq {
		if err != nil {
			lastErr = err
			continue
		}
		if resp != nil {
			responses = append(responses, resp)
		}
	}
	return responses, lastErr
}
