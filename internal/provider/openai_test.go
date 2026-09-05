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
	"sync"
	"testing"
)

func strictToolList() []Tool {
	return []Tool{
		{Type: "function", Function: ToolFunction{
			Name:        "bash",
			Description: "run a shell command",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties":           map[string]any{"command": map[string]any{"type": "string"}},
				"required":             []any{"command"},
			},
		}},
		{Type: "function", Function: ToolFunction{
			Name:        "read",
			Description: "read a file",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties":           map[string]any{"path": map[string]any{"type": "string"}},
				"required":             []any{"path"},
			},
		}},
	}
}

func TestOpenAIStreamsChatCompletions(t *testing.T) {
	t.Parallel()
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
		_, _ = w.Write(fixture)
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

func TestOpenAIEmitsGenerationBudget(t *testing.T) {
	t.Parallel()
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
		_, _ = w.Write(fixture)
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

	zero := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "max_completion_tokens") {
			t.Errorf("no-budget request leaked max_completion_tokens: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
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

func TestOpenAIEmitsToolSchemaEnforcement(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		tools, _ := parsed["tools"].([]any)
		if len(tools) != 2 {
			t.Fatalf("tools = %d, want 2", len(tools))
		}
		for i, tool := range tools {
			fn, ok := tool.(map[string]any)["function"].(map[string]any)
			if !ok {
				t.Fatalf("tool %d missing function wrapper", i)
			}
			if strict, _ := fn["strict"].(bool); !strict {
				t.Errorf("tool %d function.strict = %v, want true", i, fn["strict"])
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:                 "deepseek-v4-flash",
		Messages:              []Message{},
		Tools:                 strictToolList(),
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
}

func TestOpenAIOmitsToolSchemaEnforcementByDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "strict") {
			t.Errorf("ordinary request leaked strict tool marker: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{},
		Tools:    strictToolList(),
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
}

func TestOpenAIOptsDeepseekSessionCache(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"prompt_cache_key":"sess-123"`) {
			t.Errorf("request body missing prompt_cache_key: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
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

func TestOpenAIStreamsPromptCacheUsage(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/usage-cache.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(fixture)
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

func TestOpenAIUsageWithoutCacheKeys(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/usage-nocache.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(fixture)
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
	if usage.PromptCacheHitTokens != 0 {
		t.Errorf("hit = %d, want 0 (no fake hit when cache keys absent)", usage.PromptCacheHitTokens)
	}
	if usage.PromptCacheMissTokens != usage.PromptTokens {
		t.Errorf("miss = %d, want %d (all input billed cold)", usage.PromptCacheMissTokens, usage.PromptTokens)
	}
}

func TestOpenAIUsagePartialCacheKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fixture  string
		wantHit  int
		wantMiss int
	}{
		{name: "hit-only", fixture: "testdata/usage-cache-hitonly.sse", wantHit: 80, wantMiss: 20},
		{name: "miss-only", fixture: "testdata/usage-cache-missonly.sse", wantHit: 0, wantMiss: 30},
		{name: "openaishape", fixture: "testdata/usage-openaishape.sse", wantHit: 80, wantMiss: 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				_, _ = w.Write(fixture)
			}))
			cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
			s, err := cl.Stream(context.Background(), Request{Model: "deepseek-v4-flash", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
			srv.Close()
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
			if usage.PromptCacheHitTokens != tc.wantHit {
				t.Errorf("hit = %d, want %d", usage.PromptCacheHitTokens, tc.wantHit)
			}
			if usage.PromptCacheMissTokens != tc.wantMiss {
				t.Errorf("miss = %d, want %d", usage.PromptCacheMissTokens, tc.wantMiss)
			}
		})
	}
}

func TestOpenAIMalformedEventReturnsCleanError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: this is not json\n\n"))
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

func TestAssistantMessageAlwaysCarriesReasoningContent(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(Message{Role: RoleAssistant, Content: "resumed"})
	if err != nil {
		t.Fatalf("Marshal(assistant, empty reasoning) error = %v", err)
	}
	if !bytes.Contains(body, []byte(`"reasoning_content":""`)) {
		t.Fatalf("assistant message body %s dropped empty reasoning_content (DeepSeek 400 risk)", body)
	}

	full, err := json.Marshal(Message{Role: RoleAssistant, Content: "final", ReasoningContent: "think carefully"})
	if err != nil {
		t.Fatalf("Marshal(assistant, real reasoning) error = %v", err)
	}
	if !bytes.Contains(full, []byte(`"reasoning_content":"think carefully"`)) {
		t.Fatalf("assistant message body %s lost real reasoning_content", full)
	}
}

func TestOpenAIDeclaresGenerationControlCapabilities(t *testing.T) {
	t.Parallel()
	cl := NewOpenAICompatible("k", "http://example.invalid/v1/chat/completions")
	supp, err := cl.SupportedGenerationControls(context.Background())
	if err != nil {
		t.Fatalf("SupportedGenerationControls() error = %v, want nil", err)
	}
	want := []GenerationControl{GenerationControlGenerationBudget, GenerationControlToolSchemaEnforcement, GenerationControlThinkingSuppression}
	if len(supp) != len(want) {
		t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
	}
	for i := range want {
		if supp[i] != want[i] {
			t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
		}
	}
}

func TestOpenAIEmitsThinkingAndReasoningEffort(t *testing.T) {
	t.Parallel()
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
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	if _, err := cl.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		ThinkingEnabled: true,
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
}

func TestOpenAICapabilityMatchesWireBehavior(t *testing.T) {
	t.Parallel()
	cl := NewOpenAICompatible("k", "http://example.invalid/v1/chat/completions")
	assertSuppressionHonored(t, cl)
	streamAssertSuppression(t, func(url string) Provider {
		return NewOpenAICompatible("k", url)
	}, "opencode-go")
}

func TestOpenAIOmitsThinkingWhenDisabled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		if parsed["thinking"] != nil {
			t.Errorf("request body %s has thinking control, want omitted when off", body)
		}
		if parsed["reasoning_effort"] != nil {
			t.Errorf("request body %s has reasoning_effort, want omitted when thinking off", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	if _, err := cl.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		ThinkingEnabled: false,
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
}

func TestNormalizeReasoningEffort(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"xhigh":  "xhigh",
		"max":    "max",
		"":       "",
		"bogus":  "bogus",
	}
	for in, want := range cases {
		if got := NormalizeReasoningEffort(in); got != want {
			t.Errorf("NormalizeReasoningEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenCodeGoSendsStableSessionHeader(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Opencode-Session"); got != "sess-123" {
			t.Errorf("X-Opencode-Session = %q, want %q", got, "sess-123")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenCodeGo("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:      "deepseek-v4-flash",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		SessionKey: "sess-123",
	})
	if err != nil {
		t.Fatalf("OpenCodeGo.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
}

func TestOpenAICompatibleSendsEitriUserAgent(t *testing.T) {
	t.Parallel()

	// Verify the identity User-Agent is stamped on both the streaming chat
	// call and model discovery, not just one path.
	var mu sync.Mutex
	var chatUA, modelsUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "/models") {
			modelsUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		chatUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenCodeGo("test-key", srv.URL)
	if _, err := cl.Models(context.Background()); err != nil {
		t.Fatalf("OpenCodeGo.Models() error = %v, want nil", err)
	}
	s, err := cl.Stream(context.Background(), Request{
		Model:      "deepseek-v4-flash",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		SessionKey: "sess-123",
	})
	if err != nil {
		t.Fatalf("OpenCodeGo.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if modelsUA != eitriUserAgent {
		t.Errorf("models User-Agent = %q, want %q", modelsUA, eitriUserAgent)
	}
	if chatUA != eitriUserAgent {
		t.Errorf("stream User-Agent = %q, want %q", chatUA, eitriUserAgent)
	}
}
