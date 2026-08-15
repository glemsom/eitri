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

// ErrNoDiscovery is returned by the provider model-discovery seam when the
// configured provider has no ModelLister capability (or none is set). The
// Settings panel surfaces it as the discovery error state (issue #89 AC2) so
// model discovery never fails the TUI boot silently.
var ErrNoDiscovery = errors.New("provider does not support model discovery")

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
//
// The struct fields are the internal reassembled form. MarshalJSON emits the
// Chat Completions transport shape: the name+arguments nested under a
// `function` object beside id/type. The OpenCode Go gateway rejects a resubmitted
// assistant tool_calls entry that lacks the nested function object
// ("missing field `function`") with a 400/401, so the wire must carry
// {"id","type","function":{"name","arguments"}} (docs/research/tool-exposure.md §2).
type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// toolCallWire is the deterministic wire shape for a resubmitted assistant
// tool call: id/type at the top level, name+arguments nested under function.
type toolCallWire struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// MarshalJSON serializes a ToolCall into the Chat Completions assistant
// tool_calls element shape. The function sub-object is always present, so the
// round-tripped assistant history the provider requires is preserved.
func (t ToolCall) MarshalJSON() ([]byte, error) {
	w := toolCallWire{ID: t.ID, Type: t.Type}
	w.Function.Name = t.Name
	w.Function.Arguments = t.Arguments
	return json.Marshal(w)
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
	// via NormalizeReasoningEffort before hitting the wire (low/medium/high/max
	// pass through, xhigh→high). Empty omits reasoning_effort from the body.
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

	// Sampling, when set, opts a special (non-tool) turn into a requested
	// Sampling Policy (issue #61, docs/spec.md §13): temperature- or
	// nucleus-based sampling for a constrained generation, emitted on the wire as
	// temperature or top_p respectively. A policy expresses exactly one of the two
	// modes, so a provider request can never carry both sampling fields together.
	// Kept default-nil so ordinary agent/tool turns stay on provider defaults and
	// the byte-identical request head is preserved for the prompt cache
	// (docs/spec.md §4).
	Sampling *SamplingPolicy
}

// SamplingPolicyMode identifies which wire sampling knob a special turn
// requests (docs/spec.md §13 / issue #61).
type SamplingPolicyMode string

// The two supported Sampling Policy modes.
const (
	// SamplingTemperature requests temperature-based sampling, emitted on the
	// wire as temperature.
	SamplingTemperature SamplingPolicyMode = "temperature"
	// SamplingNucleus requests nucleus (top-p) sampling, emitted on the wire
	// as top_p.
	SamplingNucleus SamplingPolicyMode = "nucleus"
)

// SamplingPolicy is a special turn's requested sampling: exactly one mode plus
// its value. A policy always selects one mode, so the wire emission derived from
// it carries temperature or top_p — never both (issue #61).
//
// Value must be in the provider's valid range: [0,2] for temperature and (0,1]
// for top_p per the OpenAI Chat-Completions contract. Validation of Value is the
// caller's responsibility; the wire helper clamps nothing and emits the float as
// given so a special turn's sampling is honored as declared.
type SamplingPolicy struct {
	Mode  SamplingPolicyMode
	Value float64
}

// NormalizeReasoningEffort forwards reasoning-effort tiers to the wire
// (docs/spec.md §6): low, medium, high and max are each first-class and pass
// through unchanged. The official create-chat-completion reference lists only
// [low, high, max] and maps medium and xhigh to high; Eitri exposes medium as
// a first-class option even though the endpoint collapses its result, so
// nothing is remapped client-side except xhigh → high. Any other value
// (including empty) is returned untouched so it can be omitted.
func NormalizeReasoningEffort(effort string) string {
	if effort == "xhigh" {
		return "high"
	}
	return effort
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

	// cacheHitAssigned/cacheMissAssigned record whether each prompt_cache_*
	// key was actually present in the parsed JSON. They distinguish an absent
	// key (an OpenCode proxy omitting the DeepSeek-native usage shape) from a
	// present-but-zero key (a fully-cached turn), which must pass through
	// unchanged. Only unmarshal touches them; callers read the public ints.
	cacheHitAssigned  bool
	cacheMissAssigned bool
}

// UnmarshalJSON decodes a Usage blob while tracking which prompt_cache_* keys
// were present, so finalize (issue #218) can tell an absent cache shape from an
// explicit hit=miss=0 one. A custom unmarshal is required because a plain
// struct cannot distinguish an omitted json key from a present zero value.
func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageWire struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var w usageWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	u.PromptTokens = w.PromptTokens
	u.CompletionTokens = w.CompletionTokens
	u.PromptCacheHitTokens = w.PromptCacheHitTokens
	u.PromptCacheMissTokens = w.PromptCacheMissTokens
	_, u.cacheHitAssigned = raw["prompt_cache_hit_tokens"]
	_, u.cacheMissAssigned = raw["prompt_cache_miss_tokens"]
	return nil
}

// finalize applies the absent-key safe-parse fallback (issue #218) so an
// OpenCode proxy that omits the DeepSeek-native prompt_cache_* shape still
// produces honest telemetry:
//   - neither prompt_cache_* key present: every input token is a cold miss
//     (Hit=0, Miss=PromptTokens). The TUI gauge reads cache:0% and cost bills
//     at full miss-rate — never a fabricated hit, never a mispriced bill.
//   - hit present, miss absent: the difference PromptTokens-Hit is the cold
//     remainder billed at miss rate, keeping an honest Hit+Miss==PromptTokens
//     denominator.
//   - miss present, hit absent: Hit stays 0 (no fake hit fabricated); the
//     reported Miss is kept as-is.
//   - both present (the DeepSeek-native shape): unchanged.
func (u *Usage) finalize() {
	if u == nil {
		return
	}
	if u.cacheHitAssigned || u.cacheMissAssigned {
		if u.PromptCacheHitTokens > 0 && u.PromptCacheHitTokens < u.PromptTokens && !u.cacheMissAssigned {
			// hit-only shape: the rest of the prompt was cold-billed. Guarded to
			// the plausible range so a degenerate hit>prompt still clamps to 0
			// miss rather than going negative.
			u.PromptCacheMissTokens = u.PromptTokens - u.PromptCacheHitTokens
		}
		return
	}
	// absent cache shape: honest all-miss.
	u.PromptCacheHitTokens = 0
	u.PromptCacheMissTokens = u.PromptTokens
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
