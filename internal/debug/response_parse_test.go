package debug

import (
	"strings"
	"testing"
)

func TestParseResponseEnrichment_OpenAIJSON(t *testing.T) {
	body := `{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "model": "gpt-4o-mini",
  "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": "hi"}}],
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 5,
    "total_tokens": 17,
    "prompt_tokens_details": {"cached_tokens": 4}
  }
}`
	usage, finishReason, model := parseResponseEnrichment([]byte(body))
	if !usage.HasTokens() {
		t.Fatal("expected usage to be parsed")
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 5 || usage.TotalTokens != 17 {
		t.Fatalf("unexpected token counts: %+v", usage)
	}
	if usage.CacheReadTokens != 4 {
		t.Fatalf("cache read tokens = %d, want 4", usage.CacheReadTokens)
	}
	if finishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", finishReason)
	}
	if model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", model)
	}
}

func TestParseResponseEnrichment_AnthropicJSON(t *testing.T) {
	body := `{
  "id": "msg_01",
  "type": "message",
  "model": "claude-sonnet-4",
  "role": "assistant",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 20,
    "output_tokens": 8,
    "cache_read_input_tokens": 6,
    "cache_creation_input_tokens": 3
  }
}`
	usage, finishReason, model := parseResponseEnrichment([]byte(body))
	if usage.PromptTokens != 20 || usage.CompletionTokens != 8 {
		t.Fatalf("unexpected token counts: %+v", usage)
	}
	if usage.CacheReadTokens != 6 || usage.CacheWriteTokens != 3 {
		t.Fatalf("unexpected cache counts: %+v", usage)
	}
	if usage.TotalTokens != 37 {
		t.Fatalf("total tokens = %d, want 37", usage.TotalTokens)
	}
	if finishReason != "end_turn" {
		t.Fatalf("finish_reason = %q, want end_turn", finishReason)
	}
	if model != "claude-sonnet-4" {
		t.Fatalf("model = %q, want claude-sonnet-4", model)
	}
}

func TestParseResponseEnrichment_OpenAISSE(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`)
		sb.WriteString("\n\n")
	}
	// Final chunk: finish_reason on the last choice delta, then the usage chunk.
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	sb.WriteString("\n\n")
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":30,"completion_tokens":11,"total_tokens":41,"prompt_tokens_details":{"cached_tokens":7}}}`)
	sb.WriteString("\n\n")
	sb.WriteString("data: [DONE]\n\n")

	usage, finishReason, model := parseResponseEnrichment([]byte(sb.String()))
	if usage.PromptTokens != 30 || usage.CompletionTokens != 11 {
		t.Fatalf("unexpected token counts: %+v", usage)
	}
	if usage.CacheReadTokens != 7 {
		t.Fatalf("cache read tokens = %d, want 7", usage.CacheReadTokens)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", finishReason)
	}
	if model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", model)
	}
}

func TestParseResponseEnrichment_AnthropicSSE(t *testing.T) {
	body := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4","usage":{"input_tokens":25,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":4}}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`
	usage, finishReason, model := parseResponseEnrichment([]byte(body))
	if usage.PromptTokens != 25 || usage.CompletionTokens != 42 {
		t.Fatalf("unexpected token counts: %+v", usage)
	}
	if usage.CacheWriteTokens != 4 {
		t.Fatalf("cache write tokens = %d, want 4", usage.CacheWriteTokens)
	}
	if finishReason != "max_tokens" {
		t.Fatalf("finish_reason = %q, want max_tokens", finishReason)
	}
	if model != "claude-sonnet-4" {
		t.Fatalf("model = %q, want claude-sonnet-4", model)
	}
}

func TestParseResponseEnrichment_StreamTailBeyondTruncation(t *testing.T) {
	// Simulate a long stream whose head (256KB) is captured but whose usage
	// chunk sits at the very end — the tail window must still find it.
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"` + strings.Repeat("a", 1024) + `"},"finish_reason":null}]}`)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	sb.WriteString("\n\n")
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
	sb.WriteString("\n\n")
	sb.WriteString("data: [DONE]\n\n")

	body := []byte(sb.String())
	if len(body) < MaxBodyBytes {
		t.Fatalf("test fixture must exceed the truncation cap, got %d bytes", len(body))
	}

	// The recorder captures the head up to MaxBodyBytes plus a tail window;
	// feed exactly those bytes to the parser like traceBody does on Close.
	combined := append(headBytes(body), tailBytes(body)...)
	usage, finishReason, _ := parseResponseEnrichment(combined)
	if usage == nil || !usage.HasTokens() {
		t.Fatal("expected usage from the truncated stream tail")
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 50 {
		t.Fatalf("unexpected token counts: %+v", usage)
	}
	if finishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", finishReason)
	}
}

func TestParseResponseEnrichment_NoUsage(t *testing.T) {
	usage, _, _ := parseResponseEnrichment([]byte(`{"id":"x","object":"error","message":"boom"}`))
	if usage.HasTokens() {
		t.Fatalf("expected no usage, got %+v", usage)
	}
}

func TestParseResponseEnrichment_Garbage(t *testing.T) {
	usage, finishReason, model := parseResponseEnrichment([]byte("not json at all\n\n\000\x01\x02"))
	if usage.HasTokens() || finishReason != "" || model != "" {
		t.Fatalf("expected empty enrichment, got usage=%+v finish=%q model=%q", usage, finishReason, model)
	}
}
