package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// wireCapture records the last request body a provider client put on the wire,
// so a test can assert what the cache capability declaration actually emitted.
type wireCapture struct {
	mu     sync.Mutex
	path   string
	body   string
	status int
}

func (c *wireCapture) record(r *http.Request) {
	payload, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path, c.body = r.URL.Path, string(payload)
}

func (c *wireCapture) last() (path, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path, c.body
}

// chatSSEServer answers every turn with a streamed Chat Completions reply and records its request body.
func chatSSEServer(t *testing.T, capture *wireCapture) *httptest.Server {
	t.Helper()
	fixture, err := os.ReadFile("testdata/usage-cache.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture)
	}))
}

func messagesSSEServer(t *testing.T, capture *wireCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(anthropicHelloSSE))
	}))
}

// customOpenAITurn is one turn shaped like the engine's: a byte-stable static
// head, a per-run workspace directive, and the moving tail, with a toolset.
func customOpenAITurn() Request {
	return Request{
		Model:           "some-model",
		StaticPrefixLen: 2,
		SetCacheKey:     true,
		SessionKey:      "sess-cache",
		Messages: []Message{
			{Role: RoleSystem, Content: "persona head"},
			{Role: RoleSystem, Content: "skill index"},
			{Role: RoleSystem, Content: "per-run workspace directive"},
			{Role: RoleUser, Content: "question"},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "bash", Parameters: map[string]any{"type": "object"}}},
			{Type: "function", Function: ToolFunction{Name: "read_file", Parameters: map[string]any{"type": "object"}}},
		},
	}
}

func TestShippedProvidersDeclareTheWireCacheCapabilities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  ProviderCapabilities
		want ProviderCapabilities
	}{
		{
			name: "custom-openai chat completions",
			got:  NewOpenAICompatible("key", "https://example.test/v1").capabilities,
			want: ProviderCapabilities(ProviderCapabilityPromptCacheKey | ProviderCapabilityPromptCacheRetention),
		},
		{
			name: "custom-openai anthropic messages",
			got:  NewOpenAICompatible("key", "https://example.test/v1/messages").capabilities,
			want: ProviderCapabilities(ProviderCapabilityCacheControl),
		},
		{
			name: "opencode-go",
			got:  NewOpenCodeGo("key", "https://example.test/v1/chat/completions").capabilities,
			want: ProviderCapabilities(ProviderCapabilityOpenCodeSessionHeader),
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s capabilities = %b, want %b", tc.name, tc.got, tc.want)
		}
	}
}

func TestCustomOpenAIChatWireEmitsPromptCacheHintWithoutBreakpoints(t *testing.T) {
	t.Parallel()
	capture := &wireCapture{}
	srv := chatSSEServer(t, capture)
	defer srv.Close()

	client := NewOpenAICompatible("key", srv.URL+"/v1/chat/completions")
	stream, err := client.Stream(context.Background(), customOpenAITurn())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, _, err := consume(stream); err != nil {
		t.Fatalf("consume() error = %v", err)
	}

	path, body := capture.last()
	if path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", path)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got := parsed["prompt_cache_key"]; got != "sess-cache" {
		t.Errorf("prompt_cache_key = %#v, want sess-cache", got)
	}
	if got := parsed["prompt_cache_retention"]; got != promptCacheRetention24h {
		t.Errorf("prompt_cache_retention = %#v, want %q", got, promptCacheRetention24h)
	}
	assertNoCacheMarkers(t, body, "prompt_cache_key", "prompt_cache_retention")
}

func TestCustomOpenAIAnthropicWireStampsCacheBreakpoints(t *testing.T) {
	t.Parallel()
	capture := &wireCapture{}
	srv := messagesSSEServer(t, capture)
	defer srv.Close()

	client := NewOpenAICompatible("key", srv.URL+"/v1/messages")
	stream, err := client.Stream(context.Background(), customOpenAITurn())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, _, err := consume(stream); err != nil {
		t.Fatalf("consume() error = %v", err)
	}

	path, body := capture.last()
	if path != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", path)
	}
	assertNoCacheMarkers(t, body, "cache_control")
	blocks := anthropicCacheBlocks(t, body)
	if want := []string{"system[1]", "messages[0].content[0]", "tools[1]"}; !equalStrings(blocks, want) {
		t.Fatalf("cache_control blocks = %v, want %v (static head, tail, last tool)", blocks, want)
	}
	for _, block := range anthropicBodyBlocks(t, body) {
		marker, ok := block.value["cache_control"]
		if !ok {
			continue
		}
		if got := marker.(map[string]any)["type"]; got != "ephemeral" {
			t.Errorf("%s cache_control.type = %#v, want ephemeral", block.name, got)
		}
		if ttl, ok := marker.(map[string]any)["ttl"]; ok {
			t.Errorf("%s cache_control.ttl = %#v, want the API default (5m); 24h is not an Anthropic TTL", block.name, ttl)
		}
	}
}

// anthropicBodyBlocks lists every cacheable Anthropic block — system blocks,
// message content blocks, and tool definitions — with a readable name.
type anthropicBlock struct {
	name  string
	value map[string]any
}

func anthropicBodyBlocks(t *testing.T, body string) []anthropicBlock {
	t.Helper()
	var parsed struct {
		System   []map[string]any `json:"system"`
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	var out []anthropicBlock
	for i, block := range parsed.System {
		out = append(out, anthropicBlock{name: "system[" + strconv.Itoa(i) + "]", value: block})
	}
	for i, msg := range parsed.Messages {
		for j, block := range msg.Content {
			out = append(out, anthropicBlock{name: "messages[" + strconv.Itoa(i) + "].content[" + strconv.Itoa(j) + "]", value: block})
		}
	}
	for i, tool := range parsed.Tools {
		out = append(out, anthropicBlock{name: "tools[" + strconv.Itoa(i) + "]", value: tool})
	}
	return out
}

func anthropicCacheBlocks(t *testing.T, body string) []string {
	t.Helper()
	var marked []string
	for _, block := range anthropicBodyBlocks(t, body) {
		if _, ok := block.value["cache_control"]; ok {
			marked = append(marked, block.name)
		}
	}
	return marked
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOpenCodeGoEmitsNoCacheMarkersOnEitherWire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model string
		server      func(*testing.T, *wireCapture) *httptest.Server
	}{
		{name: "chat completions", model: "deepseek-v4-flash", server: chatSSEServer},
		{name: "anthropic messages", model: "qwen3.8-flash", server: messagesSSEServer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &wireCapture{}
			srv := tc.server(t, capture)
			defer srv.Close()

			req := customOpenAITurn()
			req.Model = tc.model
			stream, err := NewOpenCodeGo("key", srv.URL+"/v1/chat/completions").Stream(context.Background(), req)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if _, _, err := consume(stream); err != nil {
				t.Fatalf("consume() error = %v", err)
			}
			_, body := capture.last()
			assertNoCacheMarkers(t, body)
		})
	}
}

const responsesCompletedSSE = `data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4-mini","created_at":1,"usage":{"input_tokens":7,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}},"output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}}

`

func TestCopilotEmitsNoCacheMarkersOnEitherWire(t *testing.T) {
	t.Parallel()
	chatFixture, err := os.ReadFile("testdata/usage-cache.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The turn declares every cache capability a caller could set and carries
	// markers on its messages and tools: Copilot must drop them all.
	turn := func() Request {
		req := customOpenAITurn()
		req.Capabilities = ProviderCapabilities(ProviderCapabilityPromptCacheKey | ProviderCapabilityPromptCacheRetention | ProviderCapabilityCacheControl)
		for i := range req.Messages {
			req.Messages[i].CacheControl = &CacheControl{Type: "ephemeral"}
		}
		for i := range req.Tools {
			req.Tools[i].CacheControl = &CacheControl{Type: "ephemeral"}
		}
		return req
	}

	t.Run("chat completions", func(t *testing.T) {
		capture := &wireCapture{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capture.record(r)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write(chatFixture)
		}))
		defer srv.Close()

		client := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
		stream, err := client.Stream(context.Background(), turn())
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if _, _, err := consume(stream); err != nil {
			t.Fatalf("consume() error = %v", err)
		}
		path, body := capture.last()
		if path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", path)
		}
		assertNoCacheMarkers(t, body)
	})

	t.Run("responses", func(t *testing.T) {
		capture := &wireCapture{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capture.record(r)
			w.Header().Set("Content-Type", "text/event-stream")
			if r.URL.Path == "/responses" {
				_, _ = io.WriteString(w, responsesCompletedSSE)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"unsupported_api_for_model: this model takes /responses, not /chat/completions"}`)
		}))
		defer srv.Close()

		client := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
		stream, err := client.Stream(context.Background(), turn())
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if _, _, err := consume(stream); err != nil {
			t.Fatalf("consume() error = %v", err)
		}
		path, body := capture.last()
		if path != "/responses" {
			t.Fatalf("path = %q, want /responses after the chat rejection", path)
		}
		assertNoCacheMarkers(t, body)
	})
}

// A follow-up turn's usage blob is what proves a cache hit, whether or not Eitri
// emitted a marker: both gateway shapes must surface a non-zero hit count.
func TestUsageFinalizeReportsNonZeroCacheHit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		blob string
	}{
		{name: "deepseek shape", blob: `{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}`},
		{name: "openai shape", blob: `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage Usage
			if err := json.Unmarshal([]byte(tc.blob), &usage); err != nil {
				t.Fatalf("unmarshal usage: %v", err)
			}
			usage.finalize()
			if usage.PromptCacheHitTokens != 80 {
				t.Errorf("PromptCacheHitTokens = %d, want 80", usage.PromptCacheHitTokens)
			}
			if usage.PromptCacheMissTokens != 20 {
				t.Errorf("PromptCacheMissTokens = %d, want 20", usage.PromptCacheMissTokens)
			}
		})
	}
}
