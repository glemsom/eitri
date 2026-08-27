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
		GenerationControlJSONObjectMode,
		GenerationControlSamplingPolicy,
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
		JSONObjectMode:        true,
		ToolSchemaEnforcement: true,
		Sampling:              &SamplingPolicy{Mode: SamplingTemperature, Value: 0.7},
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
		"response_format":       map[string]any{"type": "json_object"},
		"temperature":           float64(0.7),
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
	for _, absent := range []string{"prompt_cache_key", "thinking", "reasoning_effort", "max_completion_tokens", "response_format", "temperature", "top_p"} {
		if strings.Contains(string(body), `"`+absent+`"`) {
			t.Errorf("unset control %q leaked into body: %s", absent, body)
		}
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
