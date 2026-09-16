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

// anthropicHelloSSE is a minimal Anthropic Messages stream: message_start, one
// text delta, and a terminal message_stop.
const anthropicHelloSSE = `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"qwen3.8-flash","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}
`

func TestOpenCodeGoRoutesAnthropicModelToMessagesEndpoint(t *testing.T) {
	t.Parallel()
	var path, apiKey, version, session string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		apiKey = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		session = r.Header.Get("X-Opencode-Session")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(anthropicHelloSSE))
	}))
	defer srv.Close()

	cl := NewOpenCodeGo("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:      "qwen3.8-flash",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		SessionKey: "sess-1",
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	answer, _, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v", err)
	}
	if answer != "hello" {
		t.Errorf("answer = %q, want hello", answer)
	}
	if path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", path)
	}
	if apiKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", apiKey)
	}
	if version != anthropicAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", version, anthropicAPIVersion)
	}
	if session != "sess-1" {
		t.Errorf("X-Opencode-Session = %q, want sess-1", session)
	}
}

func TestOpenCodeGoRoutesChatModelToChatEndpoint(t *testing.T) {
	t.Parallel()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	cl := NewOpenCodeGo("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	answer, _, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v", err)
	}
	if answer != "hi" {
		t.Errorf("answer = %q, want hi", answer)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", path)
	}
}

func TestCustomOpenAIAnthropicURLRoutesToMessages(t *testing.T) {
	t.Parallel()
	var path, apiKey, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		apiKey = r.Header.Get("x-api-key")
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(anthropicHelloSSE))
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("custom-key", srv.URL+"/v1/messages")
	s, err := cl.Stream(context.Background(), Request{
		Model:    "any-model",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v", err)
	}
	if path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", path)
	}
	if apiKey != "custom-key" {
		t.Errorf("x-api-key = %q, want custom-key", apiKey)
	}
	if auth != "" {
		t.Errorf("Authorization = %q, want empty on the anthropic wire", auth)
	}
}

func TestOpenAICompatibleClassifiesEndpointURL(t *testing.T) {
	t.Parallel()
	chat := NewOpenAICompatible("k", "https://example.com/v1/chat/completions")
	if chat.url != "https://example.com/v1/chat/completions" || chat.anthropicURL != "" {
		t.Errorf("chat client urls = %q / %q", chat.url, chat.anthropicURL)
	}
	bare := NewOpenAICompatible("k", "https://example.com/v1")
	if bare.url != "https://example.com/v1/chat/completions" {
		t.Errorf("bare chat client url = %q", bare.url)
	}
	anthropic := NewOpenAICompatible("k", "https://example.com/v1/messages")
	if anthropic.anthropicURL != "https://example.com/v1/messages" || anthropic.url != "" {
		t.Errorf("anthropic client urls = %q / %q", anthropic.url, anthropic.anthropicURL)
	}
	if !isAnthropicMessagesURL("https://example.com/v1/messages") {
		t.Errorf("isAnthropicMessagesURL(/v1/messages) = false, want true")
	}
}

func TestAnthropicOnlyClientReportsNoDiscovery(t *testing.T) {
	t.Parallel()
	cl := NewOpenAICompatible("k", "https://example.com/v1/messages")
	if _, err := cl.Models(context.Background()); !errors.Is(err, ErrNoDiscovery) {
		t.Errorf("Models() error = %v, want ErrNoDiscovery", err)
	}
}
