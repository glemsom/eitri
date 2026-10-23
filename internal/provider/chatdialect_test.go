package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

var _ Dialect = (*ChatCompletionsDialect)(nil)

var cacheExtensions = ProviderCapabilities(ProviderCapabilityPromptCacheKey | ProviderCapabilityPromptCacheRetention | ProviderCapabilityCacheControl)

func TestChatCompletionsDialectCapabilities(t *testing.T) {
	t.Parallel()
	cl := NewOpenAICompatible("test-key", "https://example.test/v1/chat/completions")
	declared, err := cl.SupportedGenerationControls(context.Background())
	if err != nil {
		t.Fatalf("SupportedGenerationControls() error = %v, want nil", err)
	}
	want := []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlToolSchemaEnforcement,
		GenerationControlThinkingSuppression,
	}
	if len(declared) != len(want) {
		t.Fatalf("SupportedGenerationControls() = %v, want %v", declared, want)
	}
	for i := range declared {
		if declared[i] != want[i] {
			t.Fatalf("SupportedGenerationControls() = %v, want %v", declared, want)
		}
	}
}

func TestChatCompletionsDialectBuildShapesAllControls(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:                 "deepseek-v4-flash",
		Messages:              []Message{{Role: RoleUser, Content: "hi"}},
		Tools:                 strictToolList(),
		ToolChoice:            "auto",
		SetCacheKey:           true,
		SessionKey:            "sess-123",
		Capabilities:          cacheExtensions,
		ThinkingEnabled:       true,
		ReasoningEffort:       "high",
		MaxOutputTokens:       256,
		ToolSchemaEnforcement: true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	checks := map[string]any{
		"model":                 "deepseek-v4-flash",
		"stream":                true,
		"prompt_cache_key":      "sess-123",
		"thinking":              map[string]any{"type": "enabled"},
		"reasoning_effort":      "high",
		"max_completion_tokens": float64(256),
	}
	for key, want := range checks {
		if got, ok := parsed[key]; !ok || !jsonEqual(got, want) {
			t.Errorf("%q = %#v, want %#v", key, parsed[key], want)
		}
	}
}

func TestChatCompletionsDialectBuildEmitsPromptCacheHintWithoutBreakpoints(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:           "some-model",
		ProviderID:      ProviderCustomOpenAI,
		Messages:        []Message{{Role: RoleSystem, Content: "stable instructions"}, {Role: RoleUser, Content: "question"}},
		StaticPrefixLen: 1,
		SetCacheKey:     true,
		SessionKey:      "session-1",
		Capabilities:    cacheExtensions,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got := parsed["prompt_cache_key"]; got != "session-1" {
		t.Errorf("prompt_cache_key = %#v, want session-1", got)
	}
	if got := parsed["prompt_cache_retention"]; got != promptCacheRetention24h {
		t.Errorf("prompt_cache_retention = %#v, want %q", got, promptCacheRetention24h)
	}
	assertNoCacheMarkers(t, string(body), "prompt_cache_key", "prompt_cache_retention")
}

func TestChatCompletionsDialectBuildOmitsUnsetControls(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	for _, absent := range []string{"prompt_cache_key", "thinking", "reasoning_effort", "max_completion_tokens"} {
		if strings.Contains(string(body), `"`+absent+`"`) {
			t.Errorf("unset control %q leaked into body: %s", absent, body)
		}
	}
}

func TestChatCompletionsDialectBuildSetsRetentionWhenDeclared(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:        "some-model",
		Messages:     []Message{{Role: RoleUser, Content: "hi"}},
		ProviderID:   ProviderCustomOpenAI,
		Capabilities: cacheExtensions,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got, ok := parsed["prompt_cache_retention"]; !ok || got != promptCacheRetention24h {
		t.Errorf("prompt_cache_retention = %#v, want %q", parsed["prompt_cache_retention"], promptCacheRetention24h)
	}
}

func TestChatCompletionsDialectBuildOmitsRetentionForCustomOpenAI(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:      "some-model",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		ProviderID: ProviderCustomOpenAI,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	assertNoCacheMarkers(t, string(body))
}

func TestChatCompletionsDialectBuildSetsCacheControlOnMessageAndTool(t *testing.T) {
	t.Parallel()
	marker := &CacheControl{Type: "ephemeral", TTL: "1h"}
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:        "deepseek-v4-flash",
		Messages:     []Message{{Role: RoleSystem, Content: "prefix", CacheControl: marker}},
		Tools:        []Tool{{Type: "function", Function: ToolFunction{Name: "bash"}, CacheControl: marker}},
		Capabilities: cacheExtensions,
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	want := map[string]any{"type": "ephemeral", "ttl": "1h"}
	if got, ok := parsed.Messages[0]["cache_control"]; !ok || !jsonEqual(got, want) {
		t.Errorf("message cache_control = %#v, want %#v", parsed.Messages[0]["cache_control"], want)
	}
	if got, ok := parsed.Tools[0]["cache_control"]; !ok || !jsonEqual(got, want) {
		t.Errorf("tool cache_control = %#v, want %#v", parsed.Tools[0]["cache_control"], want)
	}
}

func TestChatCompletionsDialectBuildOmitsCacheControlByDefault(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleSystem, Content: "prefix"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "bash"}}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	assertNoCacheMarkers(t, string(body))
}

func TestChatCompletionsDialectBuildOmitsRetentionByDefault(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	assertNoCacheMarkers(t, string(body))
}

func TestChatCompletionsDialectStreamAccumulatesToolCalls(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/proxy-turn1.sse")
	if err != nil {
		t.Fatalf("read tool fixture: %v", err)
	}
	stream := NewChatCompletionsDialect().Stream(strings.NewReader(string(fixture)))
	var last Chunk
	for {
		ch, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		last = ch
		if len(ch.ToolCalls) > 0 {
			tc := ch.ToolCalls[0]
			if tc.Type != "function" || tc.ID == "" || tc.Name == "" {
				t.Fatalf("tool call head incomplete: %+v", tc)
			}
		}
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("terminal chunk ToolCalls = %+v, want reassembled call", last.ToolCalls)
	}
	tc := last.ToolCalls[0]
	if tc.ID != "call_d3_read" || tc.Name != "read" || !strings.Contains(tc.Arguments, `"start_line"`) {
		t.Fatalf("tool call not reassembled: %+v", tc)
	}
}

func jsonEqual(a, b any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(ab) == string(bb)
}

// The Chat Completions wire takes a routing hint, never a breakpoint: a declared
// static prefix must not produce a cache_control marker on any message.
func TestChatCompletionsDialectBuildStampsNoBreakpoints(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:           "some-model",
		ProviderID:      ProviderCustomOpenAI,
		Capabilities:    cacheExtensions,
		StaticPrefixLen: 2,
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
			{Role: RoleSystem, Content: "s2"},
			{Role: RoleUser, Content: "u1"},
			{Role: RoleAssistant, Content: "a1"},
			{Role: RoleUser, Content: "u2"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	assertNoCacheMarkers(t, string(body), "prompt_cache_retention")
}
