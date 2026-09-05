// Package provider defines canonical requests and streaming provider adapters.
package provider

import (
	"context"
	"encoding/json"
	"errors"
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

type Message struct {
	Role             Role
	Content          string
	ToolCallID       string
	ToolCalls        []ToolCall
	ReasoningContent string
	CacheControl     *CacheControl
}

// MarshalJSON serializes a Message with role-aware reasoning handling: the `reasoning_content` field is emitted unconditionally on assistant messages (even when empty — DeepSeek's hard 400-avoidance) and omitted on every other role. `content` is emitted for tool messages even when empty, because the Chat Completions API requires it there.
func (m Message) MarshalJSON() ([]byte, error) {
	var content *string
	if m.Content != "" || m.Role == RoleTool {
		content = &m.Content
	}
	wire := messageWire{
		Role:       m.Role,
		Content:    content,
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
	Content          *string       `json:"content,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ReasoningContent *string       `json:"reasoning_content,omitempty"`
	CacheControl     *CacheControl `json:"cache_control,omitempty"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

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

const (
	ProviderOpenCodeGo   ProviderID = "opencode-go"
	ProviderCopilot      ProviderID = "github-copilot"
	ProviderCustomOpenAI ProviderID = "custom-openai"
)

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

	ToolSchemaEnforcement bool
}

// NormalizeReasoningEffort forwards reasoning-effort tiers to the wire, passing low, medium, high and max through unchanged.
func NormalizeReasoningEffort(effort string) string {
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

// UnmarshalJSON decodes a Usage blob from either cache shape the OpenCode/DeepSeek gateway emits: the DeepSeek prompt_cache_* keys, or the OpenAI prompt_tokens_details.cached_tokens shape. It records which cache key was present so finalize can tell an absent cache shape from an explicit hit=miss=0 one.
func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageWire struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		PromptTokensDetails   *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
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
	if w.PromptTokensDetails != nil && w.PromptTokensDetails.CachedTokens > 0 {
		u.PromptCacheHitTokens = w.PromptTokensDetails.CachedTokens
		u.cacheHitAssigned = true
	}
	return nil
}

// finalize reconciles the parsed blob into honest hit/miss totals: when either cache shape was present it is kept (a hit-only blob has its miss inferred as PromptTokens - Hit), and when no cache shape arrived at all every input token is a cold miss (Hit=0, Miss=PromptTokens).
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

type Stream interface {
	Next() (Chunk, error)
}

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

type ModelInfo struct {
	ID           string
	EndpointKind EndpointKind
}

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
