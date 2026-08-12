// Package provider defines the provider seam — the single, highest test seam
// in the project — plus a deterministic fake Chat-Completions SSE provider
// driven by committed fixtures, and an OpenAI-compatible Chat-Completions
// client that talks to OpenCode Go (the primary provider, docs/research/
// opencode-endpoints.md §5).
//
// Every run-engine turn goes through a Stream; TUI and batch both consume it.
package provider

import (
	"context"
	"errors"
	"io"
)

// ErrMalformed is returned when an SSE event's data is not valid JSON on a wire
// that requires it. It is a clean error, never a panic.
var ErrMalformed = errors.New("malformed Chat Completions SSE event")

// Role is a Chat-Completions message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single conversation turn sent to the provider.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent is deepseek-family chain-of-thought. It must always be
	// echoed on assistant messages so tool-call turns do not trip the provider's
	// 400 (docs/spec.md §6).
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// ToolFunction is one tool's reusable definition: a name, description, and a
// JSON-Schema parameters object. It is the canonical form re-expressed per
// wire dialect later (T5); for this ticket Chat-Completions only.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Tool is the outer Chat-Completions tool wrapper (type: function).
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolCall is one assistant-invoked function call, streamed as fragments and
// assembled into this complete form. Arguments is the raw JSON string.
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Request is one Chat-Completions turn.
type Request struct {
	Model      string
	Messages   []Message
	Tools      []Tool
	ToolChoice any
}

// Chunk is one parsed piece of a streamed turn.
type Chunk struct {
	// Content is assistant answer text emitted this chunk (delta.content).
	Content string
	// ReasoningContent is chain-of-thought text (delta.reasoning_content).
	// Present on deepseek-family streams; surface it separately, never merged
	// into Content (docs/spec.md §6).
	ReasoningContent string
	// Done is true after the terminal data: [DONE]; the turn is complete.
	Done bool
	// Usage, when non-nil, carries per-turn token telemetry delivered via
	// stream_options.include_usage.
	Usage *Usage
	// FinishReason is the streaming finish_reason, e.g. "stop" or "tool_calls".
	// A tool_calls finish signals the engine to dispatch and continue.
	FinishReason string
	// ToolCalls holds the tool calls accumulated across this turn's chunks
	// (fragmented function.name/arguments reassembled per index). Populated on
	// the terminal chunk when the model elected to call a tool.
	ToolCalls []ToolCall
}

// Usage is per-turn token telemetry, parsed at the provider seam.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Stream is the provider seam: a single turn's streamed chunks. A Stream must
// parse every SSE line into chunks, never panic on malformed data (a clean
// ErrMalformed instead), and always terminate with a Done chunk followed by
// io.EOF.
type Stream interface {
	Next() (Chunk, error)
}

// Provider opens a streamed Chat-Completions turn for req. Implementations
// include the fake fixture provider and the OpenAI-compatible HTTP client.
type Provider interface {
	Stream(ctx context.Context, req Request) (Stream, error)
}

// consume reads a Stream to completion, returning the concatenated assistant
// answer content and the terminal usage, if any. A Done chunk always precedes
// io.EOF.
func consume(s Stream) (string, *Usage, error) {
	var answer string
	var usage *Usage
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return answer, usage, nil
		}
		if err != nil {
			return "", nil, err
		}
		answer += c.Content
		if c.Usage != nil {
			usage = c.Usage
		}
		if c.Done {
			return answer, usage, nil
		}
	}
}
