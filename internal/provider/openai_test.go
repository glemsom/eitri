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

// strictToolList returns the canonical strict-shaped Chat-Completions tool
// manifest the tool-schema-enforcement wire tests assert on: two strict-shaped
// function tools (issue #62).
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

// TestOpenAIEmitsGenerationBudget verifies the request head carries
// max_completion_tokens when the caller opts into a Generation Budget, and that
// ordinary turns with no budget omit the field entirely (bytes stay clean for
// the shared request head, issue #60).
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

	// An ordinary turn with no budget must not leak the field.
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

// TestOpenAIEmitsToolSchemaEnforcement verifies that a request opted into
// provider-side Tool Schema Enforcement (issue #62) re-emits the tool manifest
// with strict:true on each tool function so a supporting provider enforces the
// JSON-Schema at generation time; strict lives beside the parameters in the
// function wrapper, exactly as the OpenAI structured-output wire expects.
func TestOpenAIEmitsToolSchemaEnforcement(t *testing.T) {
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
				t.Errorf("tool %d function.strict = %v, want true (issue #62)", i, fn["strict"])
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

// TestOpenAIOmitsToolSchemaEnforcementByDefault verifies the default wire shape
// for an ordinary agent/tool turn: no strict marker on any tool function, so the
// request head stays byte-identical to the pre-enforcement surface
// (issue #62).
func TestOpenAIOmitsToolSchemaEnforcementByDefault(t *testing.T) {
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

// TestOpenAIEmitsJSONObjectMode verifies that a JSON-Object-Mode finalization
// turn (issue #59) carries response_format:{type:json_object} on the wire, and
// that an ordinary turn with the flag off omits the field entirely (bytes stay
// clean for the shared request head).
func TestOpenAIEmitsJSONObjectMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "\"response_format\":{\"type\":\"json_object\"}") {
			t.Errorf("request body missing JSON Object Mode: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:          "deepseek-v4-flash",
		Messages:       []Message{{Role: RoleUser, Content: "finalize as JSON"}},
		JSONObjectMode: true,
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}

	// An ordinary turn with JSON Object Mode off must not leak the field.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "response_format") {
			t.Errorf("ordinary request leaked response_format: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer plain.Close()

	cl0 := NewOpenAICompatible("test-key", plain.URL+"/v1/chat/completions")
	s0, err := cl0.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() (ordinary) error = %v, want nil", err)
	}
	if _, _, err := consume(s0); err != nil {
		t.Fatalf("consume (ordinary) error = %v, want nil", err)
	}
}

// TestOpenAIEmitsSamplingPolicy verifies the Sampling Policy special-turn wire
// (issue #61): a temperature policy emits `temperature` and
// never `top_p`; a nucleus (top-p) policy emits `top_p` and never `temperature`;
// an ordinary turn with no policy emits neither field, so the shared request head
// stays untouched. The two sampling modes are mutually exclusive on the wire.
func TestOpenAIEmitsSamplingPolicy(t *testing.T) {
	// A sample float value >1.0 catches a stray temperature/top_p mis-reuse: the
	// policy value must round-trip unchanged so the provider applies the caller's
	// sampling, not a reinterpretation.
	const wantValue = 0.82

	tempSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"temperature":0.82`) {
			t.Errorf("temperature request body missing temperature: %s", body)
		}
		if strings.Contains(string(body), "top_p") {
			t.Errorf("temperature request leaked top_p alongside temperature: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer tempSrv.Close()

	cl := NewOpenAICompatible("test-key", tempSrv.URL+"/v1/chat/completions")
	s, err := cl.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "sample with temperature"}},
		Sampling: &SamplingPolicy{Mode: SamplingTemperature, Value: wantValue},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() temperature error = %v, want nil", err)
	}
	if _, _, err := consume(s); err != nil {
		t.Fatalf("consume temperature error = %v, want nil", err)
	}

	// A nucleus (top-p) policy must emit top_p and never temperature.
	const wantTopP = 0.95
	topPSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"top_p":0.95`) {
			t.Errorf("top_p request body missing top_p: %s", body)
		}
		if strings.Contains(string(body), "temperature") {
			t.Errorf("top_p request leaked temperature alongside top_p: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer topPSrv.Close()

	cl1 := NewOpenAICompatible("test-key", topPSrv.URL+"/v1/chat/completions")
	s1, err := cl1.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "sample with nucleus"}},
		Sampling: &SamplingPolicy{Mode: SamplingNucleus, Value: wantTopP},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() top_p error = %v, want nil", err)
	}
	if _, _, err := consume(s1); err != nil {
		t.Fatalf("consume top_p error = %v, want nil", err)
	}

	// An ordinary turn with no sampling policy must not leak either field.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "temperature") {
			t.Errorf("ordinary request leaked temperature: %s", body)
		}
		if strings.Contains(string(body), "top_p") {
			t.Errorf("ordinary request leaked top_p: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	defer plain.Close()

	cl0 := NewOpenAICompatible("test-key", plain.URL+"/v1/chat/completions")
	s0, err := cl0.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "plain turn"}},
	})
	if err != nil {
		t.Fatalf("OpenAI.Stream() (ordinary) error = %v, want nil", err)
	}
	if _, _, err := consume(s0); err != nil {
		t.Fatalf("consume (ordinary) error = %v, want nil", err)
	}
}

// TestOpenAIOptsDeepseekSessionCache verifies that when a Request asks for the
// deepseek session cache (SetCacheKey + SessionKey), the Chat-Completions body
// carries prompt_cache_key:<sessionID> so the gateway can hit on a stable
// byte-identical prefix (research/opencode-endpoints.md §4).
func TestOpenAIOptsDeepseekSessionCache(t *testing.T) {
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

// TestOpenAIStreamsPromptCacheUsage verifies the streamed usage carries the
// deepseek prompt-cache hit/miss token telemetry parsed at the provider seam.
func TestOpenAIStreamsPromptCacheUsage(t *testing.T) {
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

// TestOpenAIUsageWithoutCacheKeys verifies the absent-key safe-parse hardening
// (issue #218): a usage blob that carries only prompt_tokens — an OpenCode
// proxy omitting the DeepSeek-native prompt_cache_* shape — reports an honest
// Hit=0 with every input token treated as a cold Miss. No fake hit is ever
// fabricated, so the TUI gauge reads cache:0% and the cost line bills full
// miss-rate pricing instead of collapsing to an indeterminate ratio.
func TestOpenAIUsageWithoutCacheKeys(t *testing.T) {
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

// TestOpenAIUsagePartialCacheKeys covers a proxy that emits only one of the
// two DeepSeek-native prompt_cache_* keys (issue #218, acceptance (b)). No
// fake hit is ever fabricated:
//   - hit-only: the reported hits are kept, and the remaining prompt tokens are
//     inferred as cold misses so Hit+Miss still equals PromptTokens (honest
//     denominator for the gauge).
//   - miss-only: Hit stays 0 (a hit is never invented), and the reported miss
//     count is honored as-is.
func TestOpenAIUsagePartialCacheKeys(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		wantHit  int
		wantMiss int
	}{
		{name: "hit-only", fixture: "testdata/usage-cache-hitonly.sse", wantHit: 80, wantMiss: 20},
		{name: "miss-only", fixture: "testdata/usage-cache-missonly.sse", wantHit: 0, wantMiss: 30},
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

// TestOpenAIMalformedEventReturnsCleanError verifies an invalid SSE payload is
// surfaced as ErrMalformed, never a crash.
func TestOpenAIMalformedEventReturnsCleanError(t *testing.T) {
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

// TestAssistantMessageAlwaysCarriesReasoningContent encodes the hard DeepSeek
// 400-avoidance wire guarantee: an assistant message must
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

// TestOpenAIDeclaresGenerationControlCapabilities verifies the Chat-Completions
// client advertises the generation_budget, json_object_mode, sampling_policy,
// tool_schema_enforcement, and thinking_suppression controls through the
// generation-control capability surface, so the engine can pre-flight a
// special/tool turn's requirements (issues #59–#62, #265) before any wire call.
func TestOpenAIDeclaresGenerationControlCapabilities(t *testing.T) {
	cl := NewOpenAICompatible("k", "http://example.invalid/v1/chat/completions")
	supp, err := cl.SupportedGenerationControls(context.Background())
	if err != nil {
		t.Fatalf("SupportedGenerationControls() error = %v, want nil", err)
	}
	want := []GenerationControl{GenerationControlGenerationBudget, GenerationControlJSONObjectMode, GenerationControlSamplingPolicy, GenerationControlToolSchemaEnforcement, GenerationControlThinkingSuppression}
	if len(supp) != len(want) {
		t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
	}
	for i := range want {
		if supp[i] != want[i] {
			t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
		}
	}
}

// TestOpenAIEmitsThinkingAndReasoningEffort verifies the request head carries
// DeepSeek's reasoning controls — `thinking` default-enabled and a normalized
// `reasoning_effort` — when the caller opts into them. The
// effort is normalized (xhigh→high; low/medium/high/max pass through) so the
// body emits only the tiers the primary provider accepts.
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
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	if _, err := cl.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		ThinkingEnabled: true,
		ReasoningEffort: "xhigh", // legacy; normalized to high on the wire
	}); err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
}

// TestOpenAICapabilityMatchesWireBehavior ties the advertised thinking-
// suppression control to the wire shape that honors it (issue #265 AC-4):
// negotiation honors a required thinking_suppression request, AND a
// thinking-off stream omits the thinking toggle entirely — the omission IS the
// suppression on this path (issue #54). TestOpenAIOmitsThinkingWhenDisabled
// pins the wire shape alone; this test asserts advertisement and wire agree.
func TestOpenAICapabilityMatchesWireBehavior(t *testing.T) {
	cl := NewOpenAICompatible("k", "http://example.invalid/v1/chat/completions")
	assertSuppressionHonored(t, cl)
	streamAssertSuppression(t, func(url string) Provider {
		return NewOpenAICompatible("k", url)
	}, "opencode-go")
}

// TestOpenAIOmitsThinkingWhenDisabled verifies the wire-level shape of a
// non-thinking run (issue #54): when the caller disables
// thinking, the request body must omit both the DeepSeek `thinking` toggle and
// `reasoning_effort` — exactly like the compaction summarizer's non-thinking
// summary call. This is the acceptance guarantee that lets a user turn
// reasoning off without the provider forcing chain-of-thought.
func TestOpenAIOmitsThinkingWhenDisabled(t *testing.T) {
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
		// Effort is retained in config but must be dropped from the wire when
		// thinking is off, so a re-enable restores it without a stale send.
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("OpenAI.Stream() error = %v, want nil", err)
	}
}

// TestNormalizeReasoningEffort tables the reasoning-effort tier mapping
// low, medium, high and max are first-class tiers that pass
// through unchanged; xhigh maps to high per the official API reference.
func TestNormalizeReasoningEffort(t *testing.T) {
	cases := map[string]string{
		"low":    "low",
		"medium": "medium",
		"high":   "high",
		"xhigh":  "high",
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
