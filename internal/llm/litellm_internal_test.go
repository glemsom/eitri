package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ————— classifyNetError —————

func TestClassifyNetError_ConnectionRefused(t *testing.T) {
	t.Parallel()
	err := errors.New("dial tcp 127.0.0.1:8080: connect: connection refused")
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "connection refused") {
		t.Fatalf("want 'connection refused' in message, got: %v", got)
	}
}

func TestClassifyNetError_Timeout(t *testing.T) {
	t.Parallel()
	err := errors.New("dial tcp 10.0.0.1:443: i/o timeout")
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "timed out") {
		t.Fatalf("want 'timed out' in message, got: %v", got)
	}
}

func TestClassifyNetError_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "timed out") {
		t.Fatalf("want 'timed out' in message, got: %v", got)
	}
}

func TestClassifyNetError_EOF(t *testing.T) {
	t.Parallel()
	err := errors.New("unexpected EOF")
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "LLM request failed") {
		t.Fatalf("want generic 'LLM request failed' message, got: %v", got)
	}
}

func TestClassifyNetError_ContextCanceled(t *testing.T) {
	t.Parallel()
	err := context.Canceled
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "LLM request failed") {
		t.Fatalf("want generic 'LLM request failed' message, got: %v", got)
	}
}

func TestClassifyNetError_DNS(t *testing.T) {
	t.Parallel()
	err := errors.New("dial tcp: lookup nonexistent.example.com: no such host")
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "LLM request failed") {
		t.Fatalf("want generic 'LLM request failed' message, got: %v", got)
	}
}

func TestClassifyNetError_TLS(t *testing.T) {
	t.Parallel()
	err := errors.New("tls: first record does not look like a TLS handshake")
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "LLM request failed") {
		t.Fatalf("want generic 'LLM request failed' message, got: %v", got)
	}
}

func TestClassifyNetError_WrappedTimeout(t *testing.T) {
	t.Parallel()
	inner := errors.New("i/o timeout")
	err := fmt.Errorf("request failed: %w", inner)
	got := classifyNetError(err)
	if !strings.Contains(got.Error(), "timed out") {
		t.Fatalf("want 'timed out' in message for wrapped timeout, got: %v", got)
	}
}

// ————— classifyHTTPError —————

func TestClassifyHTTPError_401_WithMessage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Invalid API key","type":"auth_error","code":"401"}}`)
	err := classifyHTTPError(401, body)
	if !strings.Contains(err.Error(), "Authentication failed") || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("want auth error with message, got: %v", err)
	}
}

func TestClassifyHTTPError_401_WithoutMessage(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(401, nil)
	if !strings.Contains(err.Error(), "Authentication failed") {
		t.Fatalf("want auth error, got: %v", err)
	}
}

func TestClassifyHTTPError_429_WithMessage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit","code":"429"}}`)
	err := classifyHTTPError(429, body)
	if !strings.Contains(err.Error(), "Rate limited") || !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Fatalf("want rate limit error with message, got: %v", err)
	}
}

func TestClassifyHTTPError_429_WithoutMessage(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(429, nil)
	if !strings.Contains(err.Error(), "Rate limited") {
		t.Fatalf("want rate limit error, got: %v", err)
	}
}

func TestClassifyHTTPError_400_ContextLength(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"This model's maximum context length is 8192 tokens","type":"invalid_request_error","code":"context_length_exceeded"}}`)
	err := classifyHTTPError(400, body)
	if !strings.Contains(err.Error(), "Context length exceeded") {
		t.Fatalf("want context length error, got: %v", err)
	}
}

func TestClassifyHTTPError_400_ContextLengthCaseInsensitive(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Context length exceeded. You can reduce the length of your messages.","type":"error","code":"bad_request"}}`)
	err := classifyHTTPError(400, body)
	if !strings.Contains(err.Error(), "Context length exceeded") {
		t.Fatalf("want context length error, got: %v", err)
	}
}

func TestClassifyHTTPError_400_Generic(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Bad request","type":"invalid_request","code":"400"}}`)
	err := classifyHTTPError(400, body)
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("want generic 400 error, got: %v", err)
	}
}

func TestClassifyHTTPError_403_WithMessage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Forbidden","type":"auth_error","code":"403"}}`)
	err := classifyHTTPError(403, body)
	if !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("want 403 error with message, got: %v", err)
	}
}

func TestClassifyHTTPError_500_WithMessage(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Internal error","type":"server_error","code":"500"}}`)
	err := classifyHTTPError(500, body)
	if !strings.Contains(err.Error(), "Server error") || !strings.Contains(err.Error(), "Internal error") {
		t.Fatalf("want server error with message, got: %v", err)
	}
}

func TestClassifyHTTPError_500_WithoutMessage(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(500, nil)
	if !strings.Contains(err.Error(), "Server error") {
		t.Fatalf("want server error, got: %v", err)
	}
}

func TestClassifyHTTPError_502(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Bad gateway","type":"server_error","code":"502"}}`)
	err := classifyHTTPError(502, body)
	if !strings.Contains(err.Error(), "Server error") || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want 502 server error, got: %v", err)
	}
}

func TestClassifyHTTPError_503(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"message":"Service unavailable","type":"server_error","code":"503"}}`)
	err := classifyHTTPError(503, body)
	if !strings.Contains(err.Error(), "Server error") || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want 503 server error, got: %v", err)
	}
}

func TestClassifyHTTPError_NonStandardBody(t *testing.T) {
	t.Parallel()
	// Body that doesn't have the expected error structure
	body := []byte(`<html><body>Not JSON</body></html>`)
	err := classifyHTTPError(500, body)
	if !strings.Contains(err.Error(), "Server error") {
		t.Fatalf("want server error even with non-JSON body, got: %v", err)
	}
}

// ————— writeLLMDebugFile —————

func TestWriteLLMDebugFile_NoDirEnv(t *testing.T) {
	t.Parallel()
	// When EITRI_DEBUG_LLM_DIR is not set, should be a no-op
	writeLLMDebugFile("http://example.com", []byte(`{"key":"val"}`), []byte(`ok`), 200, "test")
	// No assertion needed — just shouldn't panic or create files
}

func TestWriteLLMDebugFile_WritesFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EITRI_DEBUG_LLM_DIR", tmpDir)

	reqBody := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	respBody := []byte(`{"error":"internal error"}`)

	writeLLMDebugFile("http://api.example.com/chat", reqBody, respBody, 500, "chat")

	// Check that a file was created in the temp directory
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written")
	}

	// Read the last written file and verify its content
	path := filepath.Join(tmpDir, entries[len(entries)-1].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var entry struct {
		URL            string          `json:"url"`
		Request        json.RawMessage `json:"request"`
		ResponseStatus int             `json:"response_status"`
		ResponseBody   string          `json:"response_body"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if entry.URL != "http://api.example.com/chat" {
		t.Fatalf("URL = %q, want %q", entry.URL, "http://api.example.com/chat")
	}
	if entry.ResponseStatus != 500 {
		t.Fatalf("ResponseStatus = %d, want 500", entry.ResponseStatus)
	}
	if entry.ResponseBody != string(respBody) {
		t.Fatalf("ResponseBody = %q, want %q", entry.ResponseBody, string(respBody))
	}
}

func TestWriteLLMDebugFile_PrefixInFilename(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("EITRI_DEBUG_LLM_DIR", tmpDir)

	writeLLMDebugFile("http://example.com", []byte(`{}`), []byte(`{}`), 400, "stream")

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no debug file written")
	}
	name := entries[len(entries)-1].Name()
	if !strings.HasPrefix(name, "stream-llm-debug-") {
		t.Fatalf("filename = %q, want stream-llm-debug-* prefix", name)
	}
}

// ————— makeHTTPClient —————

func TestMakeHTTPClient_Nil(t *testing.T) {
	t.Parallel()
	client := makeHTTPClient(nil)
	if client != defaultHTTPClient {
		t.Fatal("expected defaultHTTPClient for nil RoundTripper")
	}
}

func TestMakeHTTPClient_DefaultTransport(t *testing.T) {
	t.Parallel()
	client := makeHTTPClient(http.DefaultTransport)
	if client != defaultHTTPClient {
		t.Fatal("expected defaultHTTPClient for http.DefaultTransport")
	}
}

func TestMakeHTTPClient_CustomTransport(t *testing.T) {
	t.Parallel()
	rt := &http.Transport{}
	client := makeHTTPClient(rt)
	if client == defaultHTTPClient {
		t.Fatal("expected new client for custom RoundTripper")
	}
	if client.Transport != rt {
		t.Fatalf("Transport = %v, want %v", client.Transport, rt)
	}
}

// ————— mapAnthropicStopReason —————

func TestMapAnthropicStopReason_EndTurn(t *testing.T) {
	t.Parallel()
	if got := mapAnthropicStopReason("end_turn"); got != "stop" {
		t.Fatalf("got %q, want %q", got, "stop")
	}
}

func TestMapAnthropicStopReason_ToolUse(t *testing.T) {
	t.Parallel()
	if got := mapAnthropicStopReason("tool_use"); got != "tool_calls" {
		t.Fatalf("got %q, want %q", got, "tool_calls")
	}
}

func TestMapAnthropicStopReason_MaxTokens(t *testing.T) {
	t.Parallel()
	if got := mapAnthropicStopReason("max_tokens"); got != "length" {
		t.Fatalf("got %q, want %q", got, "length")
	}
}

func TestMapAnthropicStopReason_Unknown(t *testing.T) {
	t.Parallel()
	if got := mapAnthropicStopReason("stop_sequence"); got != "stop_sequence" {
		t.Fatalf("got %q, want %q", got, "stop_sequence")
	}
}

func TestMapAnthropicStopReason_Empty(t *testing.T) {
	t.Parallel()
	if got := mapAnthropicStopReason(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// ————— messageToAnthropicContent —————

func TestMessageToAnthropicContent_Text(t *testing.T) {
	t.Parallel()
	s := &Anthropic{}
	msg := Message{Role: "user", Content: "Hello"}
	raw := s.messageToAnthropicContent(msg)
	// For a simple text message without tool calls or tool_call_id,
	// the content should be a JSON string
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got != "Hello" {
		t.Fatalf("got %q, want %q", got, "Hello")
	}
}

func TestMessageToAnthropicContent_ToolCall(t *testing.T) {
	t.Parallel()
	s := &Anthropic{}
	msg := Message{
		Role:    "assistant",
		Content: "Let me check the weather",
		ToolCalls: []ToolCall{
			{
				ID:   "call_123",
				Type: "function",
				Function: FunctionCall{
					Name:      "get_weather",
					Arguments: `{"location":"Paris"}`,
				},
			},
		},
	}
	raw := s.messageToAnthropicContent(msg)
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (text + tool_use)", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "Let me check the weather" {
		t.Fatalf("first block = %+v, want text block", blocks[0])
	}
	if blocks[1].Type != "tool_use" || blocks[1].ID != "call_123" || blocks[1].Name != "get_weather" {
		t.Fatalf("second block = %+v, want tool_use block", blocks[1])
	}
}

func TestMessageToAnthropicContent_ToolCallNoContent(t *testing.T) {
	t.Parallel()
	s := &Anthropic{}
	msg := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{
				ID:   "call_456",
				Type: "function",
				Function: FunctionCall{
					Name:      "search",
					Arguments: `{"q":"hello"}`,
				},
			},
		},
	}
	raw := s.messageToAnthropicContent(msg)
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (just tool_use)", len(blocks))
	}
	if blocks[0].Type != "tool_use" || blocks[0].ID != "call_456" {
		t.Fatalf("block = %+v, want tool_use block", blocks[0])
	}
}

func TestMessageToAnthropicContent_ToolResult(t *testing.T) {
	t.Parallel()
	s := &Anthropic{}
	msg := Message{
		Role:       "tool",
		Content:    `{"temperature": 22}`,
		ToolCallID: "call_123",
	}
	raw := s.messageToAnthropicContent(msg)
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 (tool_result)", len(blocks))
	}
	if blocks[0].Type != "tool_result" || blocks[0].ToolUseID != "call_123" || blocks[0].Content != `{"temperature": 22}` {
		t.Fatalf("block = %+v, want tool_result block", blocks[0])
	}
}

// ————— toAnthropicReq —————

func TestToAnthropicReq_Basic(t *testing.T) {
	t.Parallel()
	s := &Anthropic{model: "claude-3-opus"}
	req := Request{
		Model: "claude-3-opus",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}
	wire := s.toAnthropicReq(req, false)
	if wire.Model != "claude-3-opus" {
		t.Fatalf("Model = %q, want %q", wire.Model, "claude-3-opus")
	}
	if wire.MaxTokens != anthropicDefaultMaxTokens {
		t.Fatalf("MaxTokens = %d, want %d", wire.MaxTokens, anthropicDefaultMaxTokens)
	}
	if wire.Stream != false {
		t.Fatal("Stream should be false")
	}
	if len(wire.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(wire.Messages))
	}
	if wire.Messages[0].Role != "user" {
		t.Fatalf("Role = %q, want %q", wire.Messages[0].Role, "user")
	}
}

func TestToAnthropicReq_WithSystemMessage(t *testing.T) {
	t.Parallel()
	s := &Anthropic{model: "claude-3-opus"}
	req := Request{
		Model: "claude-3-opus",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		},
	}
	wire := s.toAnthropicReq(req, false)
	if wire.System != "You are a helpful assistant." {
		t.Fatalf("System = %q, want %q", wire.System, "You are a helpful assistant.")
	}
	if len(wire.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (system should be extracted)", len(wire.Messages))
	}
}

func TestToAnthropicReq_WithTools(t *testing.T) {
	t.Parallel()
	s := &Anthropic{model: "claude-3-sonnet"}
	req := Request{
		Model: "claude-3-sonnet",
		Messages: []Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []ToolDef{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				Parameters: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
			},
		},
	}
	wire := s.toAnthropicReq(req, false)
	if len(wire.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(wire.Tools))
	}
	if wire.Tools[0].Name != "get_weather" {
		t.Fatalf("Tool name = %q, want %q", wire.Tools[0].Name, "get_weather")
	}
	if wire.Tools[0].InputSchema == nil {
		t.Fatal("InputSchema should not be nil")
	}
}

func TestToAnthropicReq_StreamTrue(t *testing.T) {
	t.Parallel()
	s := &Anthropic{model: "claude-3-haiku"}
	req := Request{
		Model: "claude-3-haiku",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}
	wire := s.toAnthropicReq(req, true)
	if !wire.Stream {
		t.Fatal("Stream should be true")
	}
}

// ————— fromOpenAIResponse —————

func TestFromOpenAIResponse_Basic(t *testing.T) {
	t.Parallel()
	resp := openAIResp{
		Choices: []openAIChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: openAIRespMsg{
					Role:    "assistant",
					Content: "Hello!",
				},
			},
		},
		Usage: &Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	out := fromOpenAIResponse(resp)
	if out.Content != "Hello!" {
		t.Fatalf("Content = %q, want %q", out.Content, "Hello!")
	}
	if out.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", out.FinishReason, "stop")
	}
	if out.Usage == nil || out.Usage.TotalTokens != 30 {
		t.Fatalf("Usage = %+v, want total=30", out.Usage)
	}
}

func TestFromOpenAIResponse_NoChoices(t *testing.T) {
	t.Parallel()
	resp := openAIResp{
		Usage: &Usage{TotalTokens: 5},
	}
	out := fromOpenAIResponse(resp)
	if out.Content != "" {
		t.Fatalf("Content = %q, want empty", out.Content)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 5 {
		t.Fatalf("Usage = %+v, want total=5", out.Usage)
	}
}

func TestFromOpenAIResponse_WithToolCalls(t *testing.T) {
	t.Parallel()
	resp := openAIResp{
		Choices: []openAIChoice{
			{
				Index:        0,
				FinishReason: "tool_calls",
				Message: openAIRespMsg{
					Role:    "assistant",
					Content: "",
					ToolCalls: []openAIToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: openAIFuncCall{
								Name:      "get_weather",
								Arguments: `{"location":"Paris"}`,
							},
						},
					},
				},
			},
		},
	}
	out := fromOpenAIResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCall name = %q, want %q", out.ToolCalls[0].Function.Name, "get_weather")
	}
	if out.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want %q", out.FinishReason, "tool_calls")
	}
}

// ————— toOpenAIRequest —————

func TestToOpenAIRequest_Basic(t *testing.T) {
	t.Parallel()
	req := Request{
		Model: "gpt-4",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}
	wire := toOpenAIRequest(req)
	if wire.Model != "gpt-4" {
		t.Fatalf("Model = %q, want %q", wire.Model, "gpt-4")
	}
	if len(wire.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(wire.Messages))
	}
}

func TestToOpenAIRequest_WithSystemMessage(t *testing.T) {
	t.Parallel()
	req := Request{
		Model: "gpt-4",
		Messages: []Message{
			{Role: "system", Content: "Be helpful"},
			{Role: "user", Content: "Hello"},
		},
	}
	wire := toOpenAIRequest(req)
	if len(wire.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(wire.Messages))
	}
	if wire.Messages[0].Role != "system" || wire.Messages[0].Content != "Be helpful" {
		t.Fatalf("first message = %+v, want system/Be helpful", wire.Messages[0])
	}
}

func TestToOpenAIRequest_WithToolCallsInMessage(t *testing.T) {
	t.Parallel()
	req := Request{
		Model: "gpt-4",
		Messages: []Message{
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: FunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"Paris"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				Content:    `{"temp":22}`,
				ToolCallID: "call_1",
			},
		},
	}
	wire := toOpenAIRequest(req)
	if len(wire.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(wire.Messages))
	}
	if len(wire.Messages[0].ToolCalls) != 1 {
		t.Fatalf("got %d tool calls on first msg, want 1", len(wire.Messages[0].ToolCalls))
	}
	if wire.Messages[1].ToolCallID != "call_1" {
		t.Fatalf("ToolCallID on second msg = %q, want %q", wire.Messages[1].ToolCallID, "call_1")
	}
}

func TestToOpenAIRequest_WithToolDefs(t *testing.T) {
	t.Parallel()
	req := Request{
		Model: "gpt-4",
		Messages: []Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []ToolDef{
			{
				Name:        "get_weather",
				Description: "Get the weather",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}
	wire := toOpenAIRequest(req)
	if wire.Tools == nil {
		t.Fatal("Tools should not be nil")
	}
	toolsList, ok := wire.Tools.([]map[string]any)
	if !ok {
		t.Fatalf("Tools type = %T, want []map[string]any", wire.Tools)
	}
	if len(toolsList) != 1 {
		t.Fatalf("got %d tools, want 1", len(toolsList))
	}
}

func TestToOpenAIRequest_WithReasoningEffort(t *testing.T) {
	t.Parallel()
	req := Request{
		Model:           "o1",
		Messages:        []Message{{Role: "user", Content: "Think hard"}},
		ReasoningEffort: "high",
	}
	wire := toOpenAIRequest(req)
	if wire.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want %q", wire.ReasoningEffort, "high")
	}
}

func TestToOpenAIRequest_SessionIDTruncated(t *testing.T) {
	t.Parallel()
	longID := strings.Repeat("a", 100)
	req := Request{
		Model:     "gpt-4",
		Messages:  []Message{{Role: "user", Content: "hi"}},
		SessionID: longID,
	}
	wire := toOpenAIRequest(req)
	if len(wire.PromptCacheKey) > 64 {
		t.Fatalf("PromptCacheKey length = %d, want <= 64", len(wire.PromptCacheKey))
	}
	if wire.PromptCacheKey != longID[:64] {
		t.Fatalf("PromptCacheKey = %q, want first 64 chars", wire.PromptCacheKey)
	}
}

// ————— readOpenAIStream —————

// readOpenAIStreamFromEvents creates a pipe, writes the given SSE data,
// closes the writer, then calls readOpenAIStream and collects events.
func readOpenAIStreamFromEvents(t *testing.T, sseData string) []StreamEvent {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(sseData))
		pw.Close()
	}()

	ch := make(chan StreamEvent, 64)
	readOpenAIStream(context.Background(), pr, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func TestReadOpenAIStream_TextDeltas(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello world" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Hello world")
	}
}

func TestReadOpenAIStream_ToolCallStream(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"location\\\":\\\"Paris\\\"}\"}}]},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var toolCalls []ToolCall
	for _, evt := range events {
		if evt.Type == StreamEventTypeToolCall && len(evt.ToolCalls) > 0 {
			toolCalls = evt.ToolCalls
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool call name = %q, want %q", toolCalls[0].Function.Name, "get_weather")
	}
	if !strings.Contains(toolCalls[0].Function.Arguments, "Paris") {
		t.Fatalf("tool call args = %q, want Paris", toolCalls[0].Function.Arguments)
	}
}

func TestReadOpenAIStream_ReasoningContent(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"Let me think\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Here is the answer\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var tokens []StreamEvent
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			tokens = append(tokens, evt)
		}
	}
	if len(tokens) != 2 {
		t.Fatalf("got %d tokens, want 2", len(tokens))
	}
	if tokens[0].Content != "Let me think" || !tokens[0].IsReasoning {
		t.Fatalf("first token = %+v, want reasoning token", tokens[0])
	}
	if tokens[1].Content != "Here is the answer" || tokens[1].IsReasoning {
		t.Fatalf("second token = %+v, want non-reasoning token", tokens[1])
	}
}

func TestReadOpenAIStream_ErrorChunk(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"error\":{\"message\":\"API key invalid\",\"type\":\"auth_error\",\"code\":\"401\"}}\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "API key invalid") {
		t.Fatalf("error message = %q, want 'API key invalid'", last.Error.Error())
	}
}

func TestReadOpenAIStream_MalformedJSON(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {invalid json}\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
}

func TestReadOpenAIStream_EmptyChoices(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Hello")
	}
}

func TestReadOpenAIStream_OnlyDoneEvent(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	// Should have a Done event
	var foundDone bool
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatal("expected a Done event")
	}
}

func TestReadOpenAIStream_NoDataAfterEmpty(t *testing.T) {
	t.Parallel()
	// Empty stream — should get an error about no SSE chat completions
	events := readOpenAIStreamFromEvents(t, "")

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
}

// ————— readAnthropicStream —————

func readAnthropicStreamFromEvents(t *testing.T, sseData string) []StreamEvent {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(sseData))
		pw.Close()
	}()

	ch := make(chan StreamEvent, 64)
	readAnthropicStream(context.Background(), pr, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func TestReadAnthropicStream_TextDeltas(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello world" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Hello world")
	}
}

func TestReadAnthropicStream_ThinkingDelta(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"text\":\"Let me think\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Answer\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var tokens []StreamEvent
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			tokens = append(tokens, evt)
		}
	}
	// In the Anthropic stream reader, thinking_delta is accumulated in a buffer
	// but is NOT flushed before text_delta — it is flushed at message_delta
	// or message_stop. So the thinking text arrives after the regular text.
	if len(tokens) != 2 {
		t.Fatalf("got %d tokens, want 2 (text + thinking)", len(tokens))
	}
	// First token is the text delta emitted directly without flushing thinking
	if tokens[0].Content != "Answer" || tokens[0].IsReasoning {
		t.Fatalf("first token = %+v, want non-reasoning token with 'Answer'", tokens[0])
	}
	// Second token is the thinking flushed at message_delta before Done
	if tokens[1].Content != "Let me think" || !tokens[1].IsReasoning {
		t.Fatalf("second token = %+v, want reasoning token with 'Let me think'", tokens[1])
	}
}

func TestReadAnthropicStream_ErrorEvent(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "Overloaded") {
		t.Fatalf("error = %q, want 'Overloaded'", last.Error.Error())
	}
}

func TestReadAnthropicStream_MessageStopWithoutDelta(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hi" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Hi")
	}

	// message_stop does NOT emit a Done event — it returns early, closing the
	// channel via defer. So we should get the text token and then the channel
	// simply closes (no Done event).
	var foundDone bool
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			foundDone = true
			break
		}
	}
	if foundDone {
		t.Fatal("message_stop should not emit a Done event, it just closes the channel")
	}
}

func TestReadAnthropicStream_ToolUse(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_123\",\"name\":\"get_weather\",\"input\":{\"location\":\"Paris\"}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":10}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var toolCalls []ToolCall
	for _, evt := range events {
		if evt.Type == StreamEventTypeToolCall {
			toolCalls = append(toolCalls, evt.ToolCalls...)
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "toolu_123" {
		t.Fatalf("ToolCall ID = %q, want %q", toolCalls[0].ID, "toolu_123")
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCall name = %q, want %q", toolCalls[0].Function.Name, "get_weather")
	}
	if !strings.Contains(toolCalls[0].Function.Arguments, "Paris") {
		t.Fatalf("ToolCall args = %q, want Paris", toolCalls[0].Function.Arguments)
	}
}

func TestReadAnthropicStream_MalformedJSON(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: content_block_delta\n" +
		"data: {not valid json}\n\n"

	// Malformed JSON should be silently skipped (continue in the loop)
	events := readAnthropicStreamFromEvents(t, sse)

	// The stream should just end with a Done event since no errors propagate
	// for malformed content_block_delta (the code does 'continue')
	var foundDone bool
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			foundDone = true
			break
		}
	}
	if !foundDone {
		t.Fatal("expected a Done event even after malformed JSON (skipped)")
	}
}

func TestReadAnthropicStream_FinishReasonMapped(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":100}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var finishReason string
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			finishReason = evt.FinishReason
		}
	}
	// max_tokens -> length per mapAnthropicStopReason
	if finishReason != "length" {
		t.Fatalf("FinishReason = %q, want %q", finishReason, "length")
	}
}

// ————— Tool call ordering with non-sequential indices in OpenAI stream —————

func TestReadOpenAIStream_ToolCallNonSequentialIndices(t *testing.T) {
	t.Parallel()
	// Tool calls arrive with non-sequential indices (e.g. 5, 0, 2 — skipping 1, 3, 4)
	// Verify all three are collected regardless of arrival order.
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":5,\"id\":\"call_5\",\"type\":\"function\",\"function\":{\"name\":\"get_time\",\"arguments\":\"{\\\"tz\\\":\\\"UTC\\\"}\"}}]},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_0\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"location\\\":\\\"Paris\\\"}\"}}]},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":2,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"search_docs\",\"arguments\":\"{\\\"query\\\":\\\"api\\\"}\"}}]},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	// Collect names from the final tool call event
	var names []string
	for _, evt := range events {
		if evt.Type == StreamEventTypeToolCall && len(evt.ToolCalls) > 0 {
			names = nil // reset — only care about last emission
			for _, tc := range evt.ToolCalls {
				names = append(names, tc.Function.Name)
			}
		}
	}

	if len(names) != 3 {
		t.Fatalf("got %d tool calls %v, want 3 tool calls", len(names), names)
	}
	got := make(map[string]bool)
	for _, n := range names {
		got[n] = true
	}
	if !got["get_weather"] || !got["get_time"] || !got["search_docs"] {
		t.Fatalf("tool call names = %v, missing some of [get_weather, get_time, search_docs]", names)
	}
}

// ————— Test for toAnthropicReq with assistant role —————

func TestToAnthropicReq_AssistantMessage(t *testing.T) {
	t.Parallel()
	s := &Anthropic{model: "claude-3-opus"}
	req := Request{
		Model: "claude-3-opus",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
	}
	wire := s.toAnthropicReq(req, false)
	if len(wire.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(wire.Messages))
	}
	if wire.Messages[1].Role != "assistant" {
		t.Fatalf("second message role = %q, want %q", wire.Messages[1].Role, "assistant")
	}
}

// ————— Test fromOpenAIResponse without usage —————

func TestFromOpenAIResponse_NoUsage(t *testing.T) {
	t.Parallel()
	resp := openAIResp{
		Choices: []openAIChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: openAIRespMsg{
					Role:    "assistant",
					Content: "OK",
				},
			},
		},
	}
	out := fromOpenAIResponse(resp)
	if out.Content != "OK" {
		t.Fatalf("Content = %q, want %q", out.Content, "OK")
	}
	if out.Usage != nil {
		t.Fatalf("Usage = %+v, want nil", out.Usage)
	}
}

// ————— Test writeLLMDebugFile handles MkdirAll error gracefully —————

func TestWriteLLMDebugFile_InvalidDir(t *testing.T) {
	// Set debug dir to something that can't be created (root path without permissions)
	// On Linux, /proc/self is a directory that can't have subdirectories created
	t.Setenv("EITRI_DEBUG_LLM_DIR", "/proc/self/llm-debug")

	// This should log a warning but not panic
	writeLLMDebugFile("http://example.com", []byte(`{}`), []byte(`{}`), 500, "test")
	// No assertion — just shouldn't crash
}

// ————— Tests for toOpenAIRequest with empty SessionID —————

func TestToOpenAIRequest_EmptySessionID(t *testing.T) {
	t.Parallel()
	req := Request{
		Model:     "gpt-4",
		Messages:  []Message{{Role: "user", Content: "hi"}},
		SessionID: "",
	}
	wire := toOpenAIRequest(req)
	if wire.PromptCacheKey != "" {
		t.Fatalf("PromptCacheKey = %q, want empty", wire.PromptCacheKey)
	}
}

// ————— verify that fromOpenAIResponse preserves usage from resp —————

func TestFromOpenAIResponse_PreservesUsage(t *testing.T) {
	t.Parallel()
	resp := openAIResp{
		Choices: []openAIChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: openAIRespMsg{
					Role:    "assistant",
					Content: "Hello",
				},
			},
		},
		Usage: &Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}
	out := fromOpenAIResponse(resp)
	if out.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if out.Usage.TotalTokens != 3 {
		t.Fatalf("TotalTokens = %d, want 3", out.Usage.TotalTokens)
	}
}

// ————— toOpenAIRequest with multiple tool calls —————

func TestToOpenAIRequest_MultipleToolCalls(t *testing.T) {
	t.Parallel()
	req := Request{
		Model: "gpt-4",
		Messages: []Message{
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: FunctionCall{Name: "a", Arguments: "{}"}},
					{ID: "call_2", Type: "function", Function: FunctionCall{Name: "b", Arguments: "{}"}},
				},
			},
		},
	}
	wire := toOpenAIRequest(req)
	if len(wire.Messages[0].ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(wire.Messages[0].ToolCalls))
	}
}

// ————— Stream parsing: incomplete SSE (no double newline) —————

func TestReadOpenAIStream_IncompleteSSE(t *testing.T) {
	t.Parallel()
	// SSE data without the final double newline and no complete event
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"index\":0}]}\n"))
		pw.Close()
	}()

	ch := make(chan StreamEvent, 64)
	readOpenAIStream(context.Background(), pr, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Should get an error because no proper SSE events were completed
	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
}

// ————— ReadOpenAIStream scanner error —————

func TestReadOpenAIStream_ScannerError(t *testing.T) {
	t.Parallel()
	// Create a body that will cause a scanner error (too long token)
	// A bufio.Scanner with default buffer can't handle lines >64k
	largeLine := strings.Repeat("x", 70000)
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("data: " + largeLine + "\n\n"))
		pw.Close()
	}()

	ch := make(chan StreamEvent, 64)
	readOpenAIStream(context.Background(), pr, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	if len(events) < 1 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != StreamEventTypeError {
		t.Fatalf("last event type = %v, want StreamEventTypeError", last.Type)
	}
}

// ————— Usage delta in OpenAI stream —————

func TestReadOpenAIStream_UsageInChunk(t *testing.T) {
	t.Parallel()
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var usage *Usage
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			usage = evt.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected Usage in Done event")
	}
	if usage.TotalTokens != 15 {
		t.Fatalf("TotalTokens = %d, want 15", usage.TotalTokens)
	}
}

// ————— Reasoning buffered and flushed at end of Anthropic stream —————

func TestReadAnthropicStream_ThinkingFlushedAtEnd(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"text\":\"Thinking hard\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	// Thinking should be flushed at message_stop
	if gotText.String() != "Thinking hard" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Thinking hard")
	}
}

// ————— Stream event with data prefix that is not SSE —————

func TestReadOpenAIStream_NonDataLine(t *testing.T) {
	t.Parallel()
	// Lines without data: prefix should be ignored
	sse := "" +
		":comment\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}]}\n\n" +
		"data: [DONE]\n\n"

	events := readOpenAIStreamFromEvents(t, sse)

	var gotText strings.Builder
	for _, evt := range events {
		if evt.Type == StreamEventTypeToken {
			gotText.WriteString(evt.Content)
		}
	}
	if gotText.String() != "Hello" {
		t.Fatalf("got text %q, want %q", gotText.String(), "Hello")
	}
}

// ————— Usage in Anthropic stream Done event —————

func TestReadAnthropicStream_UsageInDone(t *testing.T) {
	t.Parallel()
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	events := readAnthropicStreamFromEvents(t, sse)

	var usage *Usage
	for _, evt := range events {
		if evt.Type == StreamEventTypeDone {
			usage = evt.Usage
		}
	}
	if usage == nil {
		t.Fatal("expected Usage in Done event")
	}
	if usage.TotalTokens != 15 {
		t.Fatalf("TotalTokens = %d, want 15 (10 input + 5 output)", usage.TotalTokens)
	}
}

// ————— Edge case: classifyHTTPError with status 200 (should not happen but test) —————

func TestClassifyHTTPError_200(t *testing.T) {
	t.Parallel()
	// 200 is not an error, but the function is only called for non-OK status codes.
	// Still test that it generates something reasonable.
	body := []byte(`{"ok":true}`)
	err := classifyHTTPError(200, body)
	if err == nil {
		t.Fatal("expected error even for 200")
	}
}

// ————— Edge case: empty body for classifyHTTPError —————

func TestClassifyHTTPError_EmptyBody(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(500, []byte{})
	if !strings.Contains(err.Error(), "Server error") {
		t.Fatalf("want Server error, got: %v", err)
	}
}
