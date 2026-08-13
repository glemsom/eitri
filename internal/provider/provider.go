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
	"encoding/json"
	"errors"
	"io"
)

// ErrMalformed is returned when an SSE event's data is not valid JSON on a wire
// that requires it. It is a clean error, never a panic.
var ErrMalformed = errors.New("malformed Chat Completions SSE event")

// ErrContextOverflow is returned by a provider when the request would overflow
// the context window (a 400/context-overflow below the proactive threshold). It
// is the emergency trigger for the session compaction engine (ADR-0003 decision
// 2, docs/spec.md §7): the engine evicts the oldest body, rebuilds the summary
// head, and retries rather than surfacing the raw overflow to the caller.
var ErrContextOverflow = errors.New("context window overflow")

// IsContextOverflow reports whether err is a context-overflow signal that
// should trigger emergency compaction: the ErrContextOverflow sentinel, or a
// non-2xx provider response whose HTTP status is a 400-level client error
// (the DeepSeek context-limit surface).
func IsContextOverflow(err error) bool {
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Code == 400
	}
	return false
}

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
	Role       Role
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
	// ReasoningContent is deepseek-family chain-of-thought. It must always be
	// echoed on assistant messages — even empty — so tool-call turns do not trip
	// the provider's 400 (docs/spec.md §6). MarshalJSON emits it on assistant
	// messages unconditionally and omits it on user/tool messages.
	ReasoningContent string
}

// MarshalJSON serializes a Message with role-aware reasoning handling: the
// `reasoning_content` field is emitted unconditionally on assistant messages
// (even when empty — DeepSeek's hard 400-avoidance, docs/spec.md §6) and
// omitted on every other role. This guarantees a resubmitted tool-call history
// always carries the field, without polluting user/tool turns with an empty
// string.
func (m Message) MarshalJSON() ([]byte, error) {
	wire := messageWire{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.ToolCalls,
	}
	// Reasoning is present on assistant messages unconditionally (even empty);
	// on every other role it is omitted so the field never pollutes the wire.
	if m.Role == RoleAssistant {
		rc := m.ReasoningContent
		wire.ReasoningContent = &rc
	}
	return json.Marshal(wire)
}

// messageWire is the deterministic field-ordered serialization shape for a
// Message. Struct field order fixes the wire key order (role, content,
// tool_call_id, tool_calls, reasoning_content) so bodies stay byte-stable for
// the prompt cache (docs/spec.md §4). ReasoningContent is a pointer: a nil
// (non-assistant) omits the field, while a non-nil assistant value always
// emits it, even when empty — the DeepSeek 400-avoidance (docs/spec.md §6).
type messageWire struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
}

// ToolFunction is one tool's reusable definition: a name, description, and a
// JSON-Schema parameters object. It is the canonical form re-expressed per
// wire dialect later (T5); for this ticket Chat-Completions only.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Strict opts this tool into provider-side Tool Schema Enforcement
	// (issue #62): serialized as strict:true beside the parameters so a
	// supporting provider rejects schema-violating tool arguments at generation
	// time. Default (false) omits the flag so ordinary manifests stay
	// byte-identical. Canonical tool authoring leaves it false and lets the
	// generation-control seam toggle enforcement across the manifest.
	Strict bool `json:"strict,omitempty"`
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
	// SetCacheKey opts the request into deepseek's session-scoped prompt cache
	// (docs/spec.md §4). When true and SessionKey is non-empty, the wire body
	// carries prompt_cache_key:<SessionKey> — treated as advisory /
	// content-addressed, kept as a namespace/telemetry key.
	SetCacheKey bool
	// SessionKey identifies the session whose stable prefix the provider should
	// cache; it disambiguates the prompt cache namespace across sessions.
	SessionKey string

	// ThinkingEnabled opts the request into DeepSeek thinking mode
	// ({"type":"enabled"}). Kept default-on for agent work; lowering effort,
	// not thinking, is how an operator trades speed (docs/spec.md §6).
	ThinkingEnabled bool
	// ReasoningEffort is the requested chain-of-thought effort level, normalized
	// via NormalizeReasoningEffort before hitting the wire (low/medium→high,
	// xhigh→max). Empty omits reasoning_effort from the body.
	ReasoningEffort string

	// MaxOutputTokens is a hard per-turn output cap for a special (non-tool)
	// turn, emitted on the wire as max_completion_tokens (issue #60). Zero is
	// the provider default: no budget requested, and ordinary agent/tool turns
	// that never set it are unaffected. It is the wire-backed Generation Budget
	// control; local output must still be capped independently as the safety
	// floor (docs/spec.md §13, ADR-0003 decision 4).
	MaxOutputTokens int

	// JSONObjectMode opts a special finalization turn into schema-constrained
	// JSON Object Mode: on a supporting provider the request carries
	// response_format:{type:json_object} so the final answer is guaranteed to be
	// a valid JSON object (issue #59, docs/spec.md §13). Kept default-off so
	// ordinary agent/tool turns never carry it and stay byte-identical.
	JSONObjectMode bool
	// ToolSchemaEnforcement opts a tool-capable turn into provider-side Tool
	// Schema Enforcement (issue #62, docs/spec.md §13): on a supporting provider
	// each tool manifest carries strict:true so the provider rejects
	// schema-violating tool arguments at generation time, in addition to Eitri's
	// mandatory local validation floor. Kept default-off so ordinary agent/tool
	// turns never carry strict and stay byte-identical; internal local
	// validation remains the safety floor regardless.
	ToolSchemaEnforcement bool
}

// NormalizeReasoningEffort maps DeepSeek's legacy effort values to the
// meaningful wire tiers (docs/spec.md §6): low/medium→high, xhigh→max. high
// and max pass through unchanged; any other value (including empty) is
// returned untouched so it can be omitted.
func NormalizeReasoningEffort(effort string) string {
	switch effort {
	case "low", "medium":
		return "high"
	case "xhigh":
		return "max"
	default:
		return effort
	}
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
// PromptCacheHitTokens/MissTokens are deepseek prompt-cache read tokens, the
// data behind the cache hit-ratio gauge and cost accounting (docs/spec.md §4).
type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`
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

// ModelLister is an optional capability a Provider may expose: discovering the
// available model IDs from the configured provider so the Settings surface can
// offer a picker without hand-editing config (eitri.md §2.2, T12). It is a
// separate interface so minimal/test providers (Scripted) need not implement
// it; callers type-assert and treat absence as "no discovery" rather than error.
type ModelLister interface {
	Models(ctx context.Context) ([]string, error)
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
