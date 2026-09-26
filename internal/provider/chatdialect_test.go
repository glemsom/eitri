package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

// compile-time proof that the Chat-Completions dialect implements the Dialect seam.
var _ Dialect = (*ChatCompletionsDialect)(nil)

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

func TestChatCompletionsDialectBuildSetsRetentionForOpenCodeGo(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:      "deepseek-v4-flash",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		ProviderID: ProviderOpenCodeGo,
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
	if strings.Contains(string(body), "prompt_cache_retention") {
		t.Errorf("custom-openai request leaked prompt_cache_retention: %s", body)
	}
}

func TestChatCompletionsDialectBuildSetsCacheControlOnMessageAndTool(t *testing.T) {
	t.Parallel()
	marker := &CacheControl{Type: "ephemeral", TTL: "1h"}
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleSystem, Content: "prefix", CacheControl: marker}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "bash"}, CacheControl: marker}},
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
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("unset cache_control leaked into body: %s", body)
	}
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
	if strings.Contains(string(body), "prompt_cache_retention") {
		t.Errorf("default request leaked prompt_cache_retention: %s", body)
	}
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

// jsonEqual reports whether a and b are JSON-equivalent values.
func jsonEqual(a, b any) bool {
	ab, aerr := json.Marshal(a)
	bb, berr := json.Marshal(b)
	return aerr == nil && berr == nil && string(ab) == string(bb)
}

// stampedBreakpoints reports the indexes of the messages a built body marks.
func stampedBreakpoints(t *testing.T, body []byte) []int {
	t.Helper()
	var parsed struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	var out []int
	for i, m := range parsed.Messages {
		if _, ok := m["cache_control"]; ok {
			out = append(out, i)
		}
	}
	return out
}

// A declared static prefix is what the caller says is byte-identical across runs,
// so the breakpoint belongs on its last message; the volatile system message that
// follows it must stay unmarked, or its per-run content lands in every cache hash.
func TestChatCompletionsDialectBuildStampsStaticPrefixEndAndTail(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:           "deepseek-v4-flash",
		ProviderID:      ProviderOpenCodeGo,
		StaticPrefixLen: 3,
		Messages: []Message{
			{Role: RoleSystem, Content: "persona head"},
			{Role: RoleSystem, Content: "skill index"},
			{Role: RoleSystem, Content: "repo instructions"},
			{Role: RoleSystem, Content: "per-run workspace directive"},
			{Role: RoleUser, Content: "u1"},
			{Role: RoleAssistant, Content: "a1"},
			{Role: RoleTool, ToolCallID: "t1", Content: "tool result"},
			{Role: RoleAssistant, Content: "a2"},
			{Role: RoleUser, Content: "u3"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	got := stampedBreakpoints(t, body)
	if want := []int{2, 8}; !reflect.DeepEqual(got, want) {
		t.Fatalf("breakpoints = %v, want %v (static prefix end, then the moving tail)", got, want)
	}
}

// Without a declared prefix the leading run of system messages stands in for it.
func TestChatCompletionsDialectBuildFallsBackToLeadingSystemMessages(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:      "deepseek-v4-flash",
		ProviderID: ProviderOpenCodeGo,
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
			{Role: RoleSystem, Content: "s2"},
			{Role: RoleUser, Content: "u1"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	if got, want := stampedBreakpoints(t, body), []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("breakpoints = %v, want %v", got, want)
	}
}

// A prefix covering the whole request leaves one breakpoint, not two on one message.
func TestChatCompletionsDialectBuildStampsOnceWhenTailIsThePrefixEnd(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:           "deepseek-v4-flash",
		ProviderID:      ProviderOpenCodeGo,
		StaticPrefixLen: 1,
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	if got, want := stampedBreakpoints(t, body), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("breakpoints = %v, want %v", got, want)
	}
}

// A declared prefix longer than the message list is a caller bug, not a crash:
// stamp nothing rather than index past the end.
func TestChatCompletionsDialectBuildIgnoresOutOfRangeStaticPrefix(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:           "deepseek-v4-flash",
		ProviderID:      ProviderOpenCodeGo,
		StaticPrefixLen: 9,
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
			{Role: RoleUser, Content: "u1"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("out-of-range static prefix still stamped a breakpoint: %s", body)
	}
}

func TestChatCompletionsDialectBuildNoBreakpointsForCustomOpenAI(t *testing.T) {
	t.Parallel()
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:      "some-model",
		ProviderID: ProviderCustomOpenAI,
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
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("custom-openai request leaked cache_control markers: %s", body)
	}
}

func TestChatCompletionsDialectBuildSkipsBreakpointsForGLM(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"glm-4.5", "zhipu-glm-4.6", "glm-4.5-flash"} {
		body, err := NewChatCompletionsDialect().Build(Request{
			Model:      model,
			ProviderID: ProviderOpenCodeGo,
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
		if strings.Contains(string(body), "cache_control") {
			t.Errorf("GLM model %q leaked cache_control markers: %s", model, body)
		}
	}
}

func TestChatCompletionsDialectBuildNoDoubleStamp(t *testing.T) {
	t.Parallel()
	marker := &CacheControl{Type: "ephemeral", TTL: "1h"}
	// user message pre-stamped; the dialect must not add markers anywhere
	body, err := NewChatCompletionsDialect().Build(Request{
		Model:      "deepseek-v4-flash",
		ProviderID: ProviderOpenCodeGo,
		Messages: []Message{
			{Role: RoleSystem, Content: "prefix", CacheControl: marker},
			{Role: RoleUser, Content: "question"},
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	var parsed struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	count := 0
	for _, m := range parsed.Messages {
		if _, ok := m["cache_control"]; ok {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d cache_control markers, want exactly 1 (the pre-stamped one)", count)
	}
}
