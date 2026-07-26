// Package litellm defines the LLM transport layer — a lightweight interface
// and adapter factory for multi-provider chat completions.
package llm

import "net/http"

// Request is a chat completion request.
type Request struct {
	Model           string
	Messages        []Message
	Tools           []ToolDef
	Stream          bool
	ReasoningEffort string // "low", "medium", "high", or "" to omit
	SessionID       string // session-scoped prompt cache key; set only when provider supports prompt caching
}

// WireMessage is the legacy wire-format message for LLM API communication.
// It is a subset of the canonical Message type and is used internally by
// adapter implementations. New code should use Message instead.
type WireMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a function call made by the LLM.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and JSON-encoded arguments of a function call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool that the LLM can call.
type ToolDef struct {
	Name        string
	Description string
	Parameters  any // JSON Schema
}

// Response is a complete (non-streaming) chat completion response.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        *Usage
}

// Usage contains token-level usage accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEventType enumerates the kinds of stream event.
type StreamEventType int

const (
	StreamEventTypeToken    StreamEventType = iota // text delta
	StreamEventTypeToolCall                        // tool call fragment
	StreamEventTypeDone                            // stream complete
	StreamEventTypeError                           // stream error
)

// StreamEvent is one event from a streaming response.
type StreamEvent struct {
	Type         StreamEventType
	Content      string     // text delta for Token events
	ToolCalls    []ToolCall // tool calls for ToolCall events
	FinishReason string     // set on Done events
	Usage        *Usage     // set on Done events (if provider sends it)
	Error        error      // set on Error events
	IsReasoning  bool       // true when Content carries reasoning/thinking text (not final output)
}

type AdapterConfig struct {
	ProviderID          string
	Model               string
	BaseURL             string
	APIKey              string
	OpenRouterRef       string            // HTTP-Referer for OpenRouter tracking
	OpenRouterTitle     string            // X-Title for OpenRouter tracking
	SupportsPromptCache bool              // provider supports prompt_cache_key field
	RoundTripper        http.RoundTripper // optional; if set, adapters use this instead of default transport

	// DebugPrompt logs full LLM request payloads at Info level when true.
	DebugPrompt bool
	// DebugRequest logs full LLM request/response payloads at Info level when true.
	DebugRequest bool
	// DebugLLMDir is the directory for writing LLM debug files on error.
	// When empty, no debug files are written.
	DebugLLMDir string
}
