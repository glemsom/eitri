package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// AnthropicDialect is the Dialect implementation for the Anthropic Messages wire
// (POST /v1/messages, x-api-key auth). It builds the Messages request body, maps
// canonical tools to Anthropic tool definitions, and reassembles the streamed
// text / thinking / tool_use content blocks back into provider chunks.
type AnthropicDialect struct{}

func NewAnthropicDialect() *AnthropicDialect { return &AnthropicDialect{} }

// anthropicDialect is the shared stateless dialect used by Anthropic-wire adapters.
var anthropicDialect = NewAnthropicDialect()

// anthropicAPIVersion is the Anthropic Messages API version header sent on every
// request; the endpoint requires it alongside the x-api-key credential.
const anthropicAPIVersion = "2023-06-01"

// anthropicMaxTokensDefault is the max_tokens a request carries when the caller
// left MaxOutputTokens unset. Unlike Chat Completions, the Anthropic Messages API
// requires max_tokens and rejects the request with a 400 when it is absent.
const anthropicMaxTokensDefault = 8192

func (d *AnthropicDialect) Build(req Request) ([]byte, error) {
	return marshalAnthropicBody(req)
}

func (d *AnthropicDialect) Capabilities() []GenerationControl {
	return []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlThinkingSuppression,
	}
}

func (d *AnthropicDialect) Manifest(defs []DialectDefinition) any {
	return anthropicToolManifest(defs)
}

func (d *AnthropicDialect) Stream(r io.Reader) Stream {
	return &anthropicStream{ev: newSSE(r), acc: newAnthropicToolAccumulator()}
}

// anthropicBody is the minimal Anthropic Messages request shape Eitri needs.
type anthropicBody struct {
	Model      string             `json:"model"`
	MaxTokens  int                `json:"max_tokens"`
	Stream     bool               `json:"stream"`
	System     []anthropicContent `json:"system,omitempty"`
	Messages   []anthropicMessage `json:"messages"`
	Tools      []anthropicTool    `json:"tools,omitempty"`
	ToolChoice any                `json:"tool_choice,omitempty"`
	Thinking   *anthropicThinking `json:"thinking,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

// anthropicMessage is one entry of the Messages array: a user/assistant turn or
// a tool_result, each holding an ordered list of content blocks.
type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent is one content block. Only the fields relevant to the block's
// type are populated; every other field is omitted so the wire stays clean.
type anthropicContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	Thinking     string        `json:"thinking,omitempty"`
	Signature    string        `json:"signature,omitempty"`
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	Input        any           `json:"input,omitempty"`
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	Content      string        `json:"content,omitempty"` // tool_result body
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *CacheControl  `json:"cache_control,omitempty"`
}

// marshalAnthropicBody builds the Anthropic Messages request body from a turn.
func marshalAnthropicBody(req Request) ([]byte, error) {
	system, messages := anthropicMessages(req.Messages)
	body := anthropicBody{
		Model:      req.Model,
		MaxTokens:  anthropicMaxTokens(req),
		Stream:     true,
		System:     system,
		Messages:   messages,
		Tools:      anthropicToolManifestFromTool(req.Tools),
		ToolChoice: anthropicToolChoice(req.ToolChoice),
	}
	if !req.ThinkingEnabled {
		body.Thinking = &anthropicThinking{Type: "disabled"}
	}
	return json.Marshal(body)
}

func anthropicMaxTokens(req Request) int {
	if req.MaxOutputTokens > 0 {
		return req.MaxOutputTokens
	}
	return anthropicMaxTokensDefault
}

// anthropicMessages splits the request's message timeline into the top-level
// system blocks and the conversational Messages array, translating each provider
// role into its Anthropic content-block form. This is the role-aware seam that
// Chat Completions' Message.MarshalJSON plays for its own wire.
func anthropicMessages(messages []Message) (system []anthropicContent, conv []anthropicMessage) {
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			system = append(system, anthropicContent{Type: "text", Text: m.Content, CacheControl: m.CacheControl})
		case RoleTool:
			conv = append(conv, anthropicMessage{
				Role: "user",
				Content: []anthropicContent{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		case RoleUser:
			conv = append(conv, anthropicMessage{
				Role:    "user",
				Content: []anthropicContent{{Type: "text", Text: m.Content, CacheControl: m.CacheControl}},
			})
		case RoleAssistant:
			// Skip an assistant turn that produced no reasoning, text, or tool_use blocks.
			// Emitting it as a bare assistant with `content: null` makes the provider reject
			// the whole request with "messages[N].content must be a string or an array of
			// content blocks" (seen when a stream ends before producing any output).
			if content := anthropicAssistantContent(m); len(content) > 0 {
				conv = append(conv, anthropicMessage{Role: "assistant", Content: content})
			}
		}
	}
	return system, conv
}

// anthropicAssistantContent folds an assistant message's reasoning, reply text,
// and tool calls into ordered Anthropic content blocks (thinking, text, tool_use).
func anthropicAssistantContent(m Message) []anthropicContent {
	var content []anthropicContent
	if m.ReasoningContent != "" {
		content = append(content, anthropicContent{Type: "thinking", Thinking: m.ReasoningContent})
	}
	if m.Content != "" {
		content = append(content, anthropicContent{Type: "text", Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		content = append(content, anthropicContent{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: toolInput(tc.Arguments)})
	}
	return content
}

// toolInput decodes a tool call's arguments JSON into a value for the Anthropic
// tool_use input block, defaulting to an empty object when the arguments are missing.
func toolInput(args string) any {
	if args == "" {
		return struct{}{}
	}
	var input any
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return struct{}{}
	}
	return input
}

// anthropicToolManifest re-expresses canonical tool definitions into the
// Anthropic tool manifest (name, description, input_schema).
func anthropicToolManifest(defs []DialectDefinition) []anthropicTool {
	out := make([]anthropicTool, 0, len(defs))
	for _, def := range defs {
		out = append(out, anthropicTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.Schema,
		})
	}
	return out
}

// anthropicToolManifestFromTool converts the Chat-Completions-shaped Tool slice
// the engine hands every dialect into the Anthropic tool manifest.
func anthropicToolManifestFromTool(tools []Tool) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name:         t.Function.Name,
			Description:  t.Function.Description,
			InputSchema:  t.Function.Parameters,
			CacheControl: t.CacheControl,
		})
	}
	return out
}

// anthropicToolChoice translates the engine's request tool_choice into the
// Anthropic shape ({type:auto|any|none|tool, name?}), preserving an already
// Anthropic-shaped value and defaulting to auto when unparseable.
func anthropicToolChoice(tc any) any {
	if tc == nil {
		return nil
	}
	if m, ok := tc.(map[string]any); ok {
		if _, hasType := m["type"]; hasType {
			return tc
		}
		if name, ok := m["name"].(string); ok && name != "" {
			return map[string]any{"type": "tool", "name": name}
		}
	}
	if s, ok := tc.(string); ok {
		switch s {
		case "auto", "any", "none", "tool":
			return map[string]any{"type": s}
		}
	}
	return map[string]any{"type": "auto"}
}

// anthropicStream adapts Anthropic Messages SSE events into the Stream seam,
// accumulating text / thinking deltas and tool_use fragments across blocks.
type anthropicStream struct {
	ev         *sse
	acc        *anthropicToolAccumulator
	stopReason string
	usage      *anthropicUsage
}

func (as *anthropicStream) Next() (Chunk, error) {
	e, err := as.ev.Next()
	if errors.Is(err, io.EOF) {
		return Chunk{}, io.EOF
	}
	if err != nil {
		return Chunk{}, err
	}
	return parseAnthropicEvent(e.data, as)
}

// anthropicToolAccumulator reassembles streamed tool_use input JSON fragments
// into complete ToolCalls, keyed by content-block index.
type anthropicToolAccumulator struct {
	calls map[int]*ToolCall
	order []int
}

func newAnthropicToolAccumulator() *anthropicToolAccumulator {
	return &anthropicToolAccumulator{calls: map[int]*ToolCall{}}
}

func (a *anthropicToolAccumulator) ensure(idx int) *ToolCall {
	call, ok := a.calls[idx]
	if !ok {
		call = &ToolCall{Type: "function"}
		a.calls[idx] = call
		a.order = append(a.order, idx)
	}
	return call
}

func (a *anthropicToolAccumulator) start(idx int, id, name string) {
	call := a.ensure(idx)
	if id != "" {
		call.ID = id
	}
	if name != "" {
		call.Name = name
	}
}

func (a *anthropicToolAccumulator) addArgs(idx int, partial string) {
	a.ensure(idx).Arguments += partial
}

func (a *anthropicToolAccumulator) finish() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		call := a.calls[idx]
		if call == nil || call.Name == "" {
			continue
		}
		out = append(out, *call)
	}
	return out
}

// anthropicEvent is the union envelope across the SSE event types Eitri consumes.
type anthropicEvent struct {
	Type         string             `json:"type"`
	Index        int                `json:"index"`
	Message      *anthropicSSEMsg   `json:"message,omitempty"`
	ContentBlock *anthropicContent  `json:"content_block,omitempty"`
	Delta        *anthropicSSEDelta `json:"delta,omitempty"`
	Usage        *anthropicUsage    `json:"usage,omitempty"`
}

type anthropicSSEMsg struct {
	Usage *anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type anthropicSSEDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

// parseAnthropicEvent turns one Anthropic SSE data payload into provider chunks.
func parseAnthropicEvent(data string, as *anthropicStream) (Chunk, error) {
	var ev anthropicEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return Chunk{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	switch ev.Type {
	case "error":
		var body map[string]any
		_ = json.Unmarshal([]byte(data), &body)
		encoded, _ := json.Marshal(body)
		return Chunk{}, &HTTPError{Code: 400, Body: string(encoded)}

	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			as.usage = ev.Message.Usage
		}
		return Chunk{}, nil

	case "content_block_start":
		if cb := ev.ContentBlock; cb != nil && cb.Type == "tool_use" {
			as.acc.start(ev.Index, cb.ID, cb.Name)
		}
		return Chunk{}, nil

	case "content_block_delta":
		switch d := ev.Delta; {
		case d == nil:
			return Chunk{}, nil
		case d.Type == "text_delta":
			return Chunk{Content: d.Text}, nil
		case d.Type == "thinking_delta":
			return Chunk{ReasoningContent: d.Thinking}, nil
		case d.Type == "input_json_delta":
			as.acc.addArgs(ev.Index, d.PartialJSON)
		}
		return Chunk{}, nil

	case "content_block_stop":
		return Chunk{}, nil

	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			as.stopReason = ev.Delta.StopReason
		}
		if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
			if as.usage == nil {
				as.usage = &anthropicUsage{}
			}
			as.usage.OutputTokens = ev.Usage.OutputTokens
		}
		return Chunk{}, nil

	case "message_stop":
		toolCalls := as.acc.finish()
		reason := "stop"
		if as.stopReason != "" {
			reason = anthropicStopReason(as.stopReason)
		}
		if len(toolCalls) > 0 {
			reason = "tool_calls"
		}
		chunk := Chunk{Done: true, ToolCalls: toolCalls, FinishReason: reason}
		if as.usage != nil {
			hit := as.usage.CacheReadInputTokens
			miss := as.usage.InputTokens - as.usage.CacheReadInputTokens
			if miss < 0 {
				miss = 0
			}
			chunk.Usage = &Usage{
				PromptTokens:          as.usage.InputTokens,
				CompletionTokens:      as.usage.OutputTokens,
				PromptCacheHitTokens:  hit,
				PromptCacheMissTokens: miss,
			}
			chunk.Usage.cacheHitAssigned = true
			chunk.Usage.cacheMissAssigned = true
			chunk.Usage.finalize()
		}
		return chunk, nil
	}
	return Chunk{}, nil
}

// anthropicStopReason maps the Anthropic stop_reason vocabulary onto Eitri's
// finish-reason strings.
func anthropicStopReason(anthropic string) string {
	switch anthropic {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}
