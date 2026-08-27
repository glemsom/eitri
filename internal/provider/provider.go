// Package provider defines the provider seam — the single, highest test seam in the project — plus a deterministic fake Chat-Completions SSE provider driven by committed fixtures, and an OpenAI-compatible Chat-Completions client that talks to OpenCode Go (the primary provider).
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ErrMalformed is returned when an SSE event's data is not valid JSON on a wire that requires it.
var ErrMalformed = errors.New("malformed Chat Completions SSE event")

// ErrContextOverflow is returned by a provider when the request would overflow the context window (a 400/context-overflow below the proactive threshold).
var ErrContextOverflow = errors.New("context window overflow")

// ErrNoDiscovery is returned by the provider model-discovery seam when the configured provider has no ModelLister capability (or none is set).
var ErrNoDiscovery = errors.New("provider does not support model discovery")

// IsContextOverflow reports whether err is a context-overflow signal that should trigger emergency compaction: the ErrContextOverflow sentinel, or a provider response whose error body names a context-limit condition (the DeepSeek/OpenCode context-overflow surface).
func IsContextOverflow(err error) bool {
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		if he.Code/100 == 4 {
			return contextOverflowBody(he.Body)
		}
	}
	return false
}

// contextOverflowBody reports whether a provider error body names a context-length / token-limit overflow condition.
func contextOverflowBody(body string) bool {
	b := strings.ToLower(body)
	signals := []string{
		"context length",
		"context window",
		"context is too long",
		"maximum context",
		"token limit",
		"tokens limit",
		"too many tokens",
		"exceeds the context",
		"exceeds the maximum",
	}
	for _, s := range signals {
		if strings.Contains(b, s) {
			return true
		}
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

// CacheControl is an Anthropic-style cache breakpoint marker serialized onto a Message or Tool.
type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// Message is a single conversation turn sent to the provider.
type Message struct {
	Role             Role
	Content          string
	ToolCallID       string
	ToolCalls        []ToolCall
	ReasoningContent string
	CacheControl     *CacheControl
}

// MarshalJSON serializes a Message with role-aware reasoning handling: the `reasoning_content` field is emitted unconditionally on assistant messages (even when empty — DeepSeek's hard 400-avoidance) and omitted on every other role.
func (m Message) MarshalJSON() ([]byte, error) {
	wire := messageWire{
		Role:       m.Role,
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.ToolCalls,
	}
	if m.Role == RoleAssistant {
		rc := m.ReasoningContent
		wire.ReasoningContent = &rc
	}
	if m.CacheControl != nil {
		wire.CacheControl = m.CacheControl
	}
	return json.Marshal(wire)
}

// messageWire is the deterministic field-ordered serialization shape for a Message.
type messageWire struct {
	Role             Role          `json:"role"`
	Content          string        `json:"content,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ReasoningContent *string       `json:"reasoning_content,omitempty"`
	CacheControl     *CacheControl `json:"cache_control,omitempty"`
}

// ToolFunction is one tool's reusable definition: a name, description, and a JSON-Schema parameters object.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

// Tool is the outer Chat-Completions tool wrapper (type: function).
type Tool struct {
	Type         string        `json:"type"`
	Function     ToolFunction  `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ToolCall is one assistant-invoked function call, streamed as fragments and assembled into this complete form.
type ToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

// toolCallWire is the deterministic wire shape for a resubmitted assistant tool call: id/type at the top level, name+arguments nested under function.
type toolCallWire struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// MarshalJSON serializes a ToolCall into the Chat Completions assistant tool_calls element shape.
func (t ToolCall) MarshalJSON() ([]byte, error) {
	w := toolCallWire{ID: t.ID, Type: t.Type}
	w.Function.Name = t.Name
	w.Function.Arguments = t.Arguments
	return json.Marshal(w)
}

// ProviderID names the provider family a turn targets, so a shared dialect can apply provider-specific caching fields without affecting another provider.
type ProviderID string

// Provider family identifiers, matching the documented families surfaced in the Settings surface.
const (
	ProviderOpenCodeGo   ProviderID = "opencode-go"
	ProviderCopilot      ProviderID = "github-copilot"
	ProviderCustomOpenAI ProviderID = "custom-openai"
)

// Request is one Chat-Completions turn.
type Request struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	ToolChoice  any
	SetCacheKey bool
	SessionKey  string
	ProviderID  ProviderID

	ThinkingEnabled bool
	ReasoningEffort string

	MaxOutputTokens int

	JSONObjectMode        bool
	ToolSchemaEnforcement bool

	Sampling *SamplingPolicy
}

// SamplingPolicyMode identifies which wire sampling knob a special turn requests.
type SamplingPolicyMode string

// The two supported Sampling Policy modes.
const (
	SamplingTemperature SamplingPolicyMode = "temperature"
	SamplingNucleus     SamplingPolicyMode = "nucleus"
)

// SamplingPolicy is a special turn's requested sampling: exactly one mode plus its value.
type SamplingPolicy struct {
	Mode  SamplingPolicyMode
	Value float64
}

// NormalizeReasoningEffort forwards reasoning-effort tiers to the wire low, medium, high and max are each first-class and pass through unchanged.
func NormalizeReasoningEffort(effort string) string {
	if effort == "xhigh" {
		return "high"
	}
	return effort
}

// Chunk is one parsed piece of a streamed turn.
type Chunk struct {
	Content          string
	ReasoningContent string
	Done             bool
	Usage            *Usage
	FinishReason     string
	ToolCalls        []ToolCall
}

// Usage is per-turn token telemetry, parsed at the provider seam.
type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`

	cacheHitAssigned  bool
	cacheMissAssigned bool
}

// UnmarshalJSON decodes a Usage blob while tracking which prompt_cache_* keys were present, so finalize can tell an absent cache shape from an explicit hit=miss=0 one.
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

// finalize applies the absent-key safe-parse fallback so an OpenCode proxy that omits the DeepSeek-native prompt_cache_* shape still produces honest telemetry: - neither prompt_cache_* key present: every input token is a cold miss (Hit=0, Miss=PromptTokens).
func (u *Usage) finalize() {
	if u == nil {
		return
	}
	if u.cacheHitAssigned || u.cacheMissAssigned {
		if u.PromptCacheHitTokens > 0 && u.PromptCacheHitTokens < u.PromptTokens && !u.cacheMissAssigned {
			u.PromptCacheMissTokens = u.PromptTokens - u.PromptCacheHitTokens
		}
		return
	}
	u.PromptCacheHitTokens = 0
	u.PromptCacheMissTokens = u.PromptTokens
}

// Stream is the provider seam: a single turn's streamed chunks.
type Stream interface {
	Next() (Chunk, error)
}

// Provider opens a streamed Chat-Completions turn for req.
type Provider interface {
	Stream(ctx context.Context, req Request) (Stream, error)
}

// EndpointKind is discovered per-model transport routing: which provider wire a model should use on first contact.
type EndpointKind string

const (
	EndpointUnknown         EndpointKind = "unknown"
	EndpointChatCompletions EndpointKind = "chat_completions"
	EndpointResponses       EndpointKind = "responses"
)

// ModelInfo is one discovered model plus any routing metadata the provider surfaced.
type ModelInfo struct {
	ID           string
	EndpointKind EndpointKind
}

// ModelIDs projects a discovered catalog to its ordered model-id list.
func ModelIDs(models []ModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

// ModelLister is an optional capability a Provider may expose: discovering the available models from the configured provider so the Settings surface can offer a picker without hand-editing config, and the runtime can learn model-specific routing metadata.
type ModelLister interface {
	Models(ctx context.Context) ([]ModelInfo, error)
}

// consume reads a Stream to completion, returning the concatenated assistant answer content and the terminal usage, if any.
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

// UnmarshalJSON decodes a ToolCall from the Chat Completions wire shape (id/type at top level, name+arguments nested under function), mirroring MarshalJSON.
func (t *ToolCall) UnmarshalJSON(data []byte) error {
	var w toolCallWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	t.ID = w.ID
	t.Type = w.Type
	t.Name = w.Function.Name
	t.Arguments = w.Function.Arguments
	return nil
}
