package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestOpenAIStreamsChatCompletions exercises the real Chat-Completions HTTP
// client against a local text/event-stream server: the request body carries
// model + messages, and the streamed deltas/usage/Done/EOF round-trip through
// the provider seam without touching the network.
func TestOpenAIStreamsChatCompletions(t *testing.T) {
	fixture, err := os.ReadFile("testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		body, _ := io.ReadAll(r.Body)
		sawAuth = string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	ctx := context.Background()
	s, err := cl.Stream(ctx, Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}

	answer, usage, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
	if answer != "Hello world" {
		t.Fatalf("answer = %q, want %q", answer, "Hello world")
	}
	if usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v, want prompt=12 completion=5", usage)
	}

	if !strings.Contains(sawAuth, `"model":"deepseek-v4-flash"`) {
		t.Errorf("request body missing model: %s", sawAuth)
	}
	if !strings.Contains(sawAuth, `"role":"user"`) {
		t.Errorf("request body missing user message: %s", sawAuth)
	}
}

// TestOpenAIOptsDeepseekSessionCache verifies that when a Request asks for the
// deepseek session cache (SetCacheKey + SessionKey), the Chat-Completions body
// carries prompt_cache_key:<sessionID> so the gateway can hit on a stable
// byte-identical prefix (docs/spec.md §4, research/opencode-endpoints.md §4).
func TestOpenAIOptsDeepseekSessionCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"prompt_cache_key":"sess-123"`) {
			t.Errorf("request body missing prompt_cache_key: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:       "deepseek-v4-flash",
		Messages:    []Message{{Role: RoleUser, Content: "hi"}},
		SetCacheKey: true,
		SessionKey:  "sess-123",
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
}

// TestOpenAIStreamsPromptCacheUsage verifies the streamed usage carries the
// deepseek prompt-cache hit/miss token telemetry parsed at the provider seam
// (docs/spec.md §4, §11).
func TestOpenAIStreamsPromptCacheUsage(t *testing.T) {
	fixture, err := os.ReadFile("testdata/usage-cache.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{Model: "deepseek-v4-flash", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	_, usage, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
	if usage == nil {
		t.Fatal("usage not parsed")
	}
	if usage.PromptCacheHitTokens != 90 || usage.PromptCacheMissTokens != 10 {
		t.Fatalf("cache usage = hit=%d miss=%d, want hit=90 miss=10", usage.PromptCacheHitTokens, usage.PromptCacheMissTokens)
	}
}

// TestOpenAIMalformedEventReturnsCleanError verifies an invalid SSE payload is
// surfaced as ErrMalformed, never a crash (docs/spec.md §11).
func TestOpenAIMalformedEventReturnsCleanError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: this is not json\n\n"))
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	_, _, err = consume(s)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("consume error = %v, want ErrMalformed", err)
	}
}
