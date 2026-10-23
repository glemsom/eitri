package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// envelopeRawValue returns the raw JSON text of one top-level envelope key.
func envelopeRawValue(t *testing.T, line, key string) (string, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", line, err)
	}
	raw, ok := fields[key]
	return strings.TrimSpace(string(raw)), ok
}

// scriptedUsageTurns scripts a two-turn run: a bash tool call whose stream
// carries several cumulative usage chunks (the last one wins per turn), then a
// final answer turn reporting its own usage.
func scriptedUsageTurns() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "final", Usage: &provider.Usage{
					PromptTokens: 4000, CompletionTokens: 300,
					PromptCacheHitTokens: 3000, PromptCacheMissTokens: 1000,
				}},
				provider.Chunk{Usage: &provider.Usage{
					PromptTokens: 4609, CompletionTokens: 435,
					PromptCacheHitTokens: 3072, PromptCacheMissTokens: 1537, PromptCacheWriteTokens: 7,
				}},
				provider.Chunk{Done: true, FinishReason: "stop"},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "one", Usage: &provider.Usage{
				PromptTokens: 1000, CompletionTokens: 20,
				PromptCacheHitTokens: 900, PromptCacheMissTokens: 100,
			}},
			provider.Chunk{Content: "two", Usage: &provider.Usage{
				PromptTokens: 3132, CompletionTokens: 92,
				PromptCacheHitTokens: 3072, PromptCacheMissTokens: 60, PromptCacheWriteTokens: 3,
			}},
			provider.Chunk{Done: true, FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_1", Name: "bash", Arguments: `{"command":"echo hello"}`},
			}},
		), nil
	})
}

func TestRunBatchJSONEnvelopeSumsUsageAcrossTurns(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	line := strings.TrimSpace(runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider:   scriptedUsageTurns(),
		Prompt:     "probe",
	}))

	raw, ok := envelopeRawValue(t, line, "usage")
	if !ok {
		t.Fatalf("envelope %q has no usage object, want the run's token totals", line)
	}
	want := `{"prompt_tokens":7741,"completion_tokens":527,"cache_hit_tokens":6144,"cache_miss_tokens":1597,"cache_write_tokens":10}`
	if raw != want {
		t.Fatalf("envelope usage = %s, want %s (sum of both turns' last usage chunk)", raw, want)
	}

	wantKeys := []string{"answer", "session", "turns", "stopped", "model", "duration_ms", "tool_calls", "usage"}
	if got := envelopeKeys(t, line); strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("envelope keys = %v, want %v", got, wantKeys)
	}
}

func TestRunBatchJSONEnvelopeOmitsUsageWhenProviderReportsNone(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	line := strings.TrimSpace(runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider: provider.NewScripted(func(context.Context, provider.Request) (provider.Stream, error) {
			return provider.StreamFunc(
				provider.Chunk{Content: "ok"},
				provider.Chunk{FinishReason: "stop", Done: true},
			), nil
		}),
	}))

	if _, ok := envelopeRawValue(t, line, "usage"); ok {
		t.Fatalf("envelope %q carries usage for a run whose provider reported none", line)
	}
	wantKeys := []string{"answer", "session", "turns", "stopped", "model", "duration_ms", "tool_calls"}
	if got := envelopeKeys(t, line); strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("envelope keys = %v, want %v", got, wantKeys)
	}
}

func TestRunBatchJSONEnvelopeUsageMatchesSessionStats(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	line := strings.TrimSpace(runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider:   scriptedUsageTurns(),
		Prompt:     "probe",
	}))

	var env struct {
		Session string         `json:"session"`
		Usage   map[string]int `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", line, err)
	}
	if env.Usage == nil {
		t.Fatalf("envelope %q has no usage object", line)
	}

	var statsOut bytes.Buffer
	if err := StatsSession(dataDir, env.Session, "json", &statsOut); err != nil {
		t.Fatalf("StatsSession(%q) error = %v", env.Session, err)
	}
	var stats map[string]json.RawMessage
	if err := json.Unmarshal(statsOut.Bytes(), &stats); err != nil {
		t.Fatalf("stats output %q is not one JSON object: %v", statsOut.String(), err)
	}

	for _, field := range []string{
		"prompt_tokens", "completion_tokens",
		"cache_hit_tokens", "cache_miss_tokens", "cache_write_tokens",
	} {
		want := 0
		if raw, ok := stats[field]; ok {
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("stats field %s = %s is not an integer: %v", field, raw, err)
			}
		}
		if got := env.Usage[field]; got != want {
			t.Errorf("envelope usage %s = %d, session stats report %d", field, got, want)
		}
	}
}

func TestRunBatchJSONEnvelopeOmitsZeroCacheSubFields(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	line := strings.TrimSpace(runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider: provider.NewScripted(func(context.Context, provider.Request) (provider.Stream, error) {
			return provider.StreamFunc(
				provider.Chunk{Content: "ok"},
				provider.Chunk{FinishReason: "stop", Done: true, Usage: &provider.Usage{
					PromptTokens: 1000, CompletionTokens: 20,
					PromptCacheHitTokens: 0, PromptCacheMissTokens: 1000,
				}},
			), nil
		}),
	}))

	raw, ok := envelopeRawValue(t, line, "usage")
	if !ok {
		t.Fatalf("envelope %q has no usage object, want the run's token totals", line)
	}
	want := `{"prompt_tokens":1000,"completion_tokens":20,"cache_miss_tokens":1000}`
	if raw != want {
		t.Fatalf("envelope usage = %s, want %s (zero cache hit and cache write omitted)", raw, want)
	}
}
