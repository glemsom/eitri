package provider

import (
	"bytes"
	"context"
	"encoding/json"
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

// TestOpenAIEmitsGenerationBudget verifies the request head carries
// max_completion_tokens when the caller opts into a Generation Budget, and that
// ordinary turns with no budget omit the field entirely (bytes stay clean for
// the shared request head, docs/spec.md §4 / issue #60).
func TestOpenAIEmitsGenerationBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "\"max_completion_tokens\":256") {
			t.Errorf("request body missing generation budget: %s", body)
		}
		if strings.Contains(string(body), "\"max_completion_tokens\":0") {
			t.Errorf("request body carried zeroed max_completion_tokens, want omitted: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		Messages:        []Message{{Role: RoleUser, Content: "summarize"}},
		MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}

	// An ordinary turn with no budget must not leak the field.
	zero := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "max_completion_tokens") {
			t.Errorf("no-budget request leaked max_completion_tokens: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		w.Write(fixture)
	}))
	defer zero.Close()

	cl0 := NewOpenAICompatible("test-key", zero.URL+"/v1/chat/completions")
	s0, err := cl0.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() (no budget) error = %v, want nil", err)
	}
	if _, _, err := consume(s0); err != nil {
		t.Fatalf("consume (no budget) error = %v, want nil", err)
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

// TestAssistantMessageAlwaysCarriesReasoningContent encodes the hard DeepSeek
// 400-avoidance wire guarantee (docs/spec.md §6): an assistant message must
// marshal `reasoning_content` even when empty, so a tool-turn against a
// reasoning provider never trips the empty-field 400. The field is never
// dropped by omitempty.
func TestAssistantMessageAlwaysCarriesReasoningContent(t *testing.T) {
	// Empty reasoning on an assistant message must still serialize the field.
	body, err := json.Marshal(Message{Role: RoleAssistant, Content: "resumed"})
	if err != nil {
		t.Fatalf("Marshal(assistant, empty reasoning) error = %v", err)
	}
	if !bytes.Contains(body, []byte(`"reasoning_content":""`)) {
		t.Fatalf("assistant message body %s dropped empty reasoning_content (DeepSeek 400 risk)", body)
	}

	// Real reasoning must round-trip on the wire unchanged.
	full, err := json.Marshal(Message{Role: RoleAssistant, Content: "final", ReasoningContent: "think carefully"})
	if err != nil {
		t.Fatalf("Marshal(assistant, real reasoning) error = %v", err)
	}
	if !bytes.Contains(full, []byte(`"reasoning_content":"think carefully"`)) {
		t.Fatalf("assistant message body %s lost real reasoning_content", full)
	}
}

// TestOpenAIDeclaresGenerationBudgetSupport verifies the OpenAI-compatible
// client advertises the generation_budget control through the generation-control
// capability surface, so the engine can pre-flight a special turn's budget
// (docs/spec.md §13 / issue #60) before any wire call.
func TestOpenAIDeclaresGenerationBudgetSupport(t *testing.T) {
	cl := NewOpenAICompatible("k", "http://example.invalid/v1/chat/completions")
	supp, err := cl.SupportedGenerationControls(context.Background())
	if err != nil {
		t.Fatalf("SupportedGenerationControls() error = %v, want nil", err)
	}
	if len(supp) != 1 || supp[0] != GenerationControlGenerationBudget {
		t.Fatalf("SupportedGenerationControls() = %v, want [generation_budget]", supp)
	}
}

// TestOpenAIEmitsThinkingAndReasoningEffort verifies the request head carries
// DeepSeek's reasoning controls — `thinking` default-enabled and a normalized
// `reasoning_effort` — when the caller opts into them (docs/spec.md §6). The
// effort is normalized (low/medium→high, xhigh→max) so the body emits only the
// meaningful tiers the primary provider accepts.
func TestOpenAIEmitsThinkingAndReasoningEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		if parsed["thinking"] == nil {
			t.Errorf("request body %s missing thinking control", body)
		}
		if eff := parsed["reasoning_effort"]; eff != "high" {
			t.Errorf("reasoning_effort = %v, want normalized high", eff)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	if _, err := cl.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		ThinkingEnabled: true,
		ReasoningEffort: "medium", // legacy; normalized to high on the wire
	}); err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
}

// TestNormalizeReasoningEffort tables the DeepSeek legacy→meaningful effort
// mapping (docs/spec.md §6): low/medium→high, xhigh→max, meaningful tiers and
// the default pass through unchanged.
func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"low":    "high",
		"medium": "high",
		"high":   "high",
		"xhigh":  "max",
		"max":    "max",
		"":       "",
	}
	for in, want := range cases {
		if got := NormalizeReasoningEffort(in); got != want {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
