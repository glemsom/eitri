package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Dialect is the seam for a single wire dialect: it owns both request shaping
// (building the wire body with the generation-control fields), tool-mapping
// (re-expressing canonical tool definitions into the dialect's tool manifest),
// and wire parsing (decoding the SSE stream back into chunks). Adapters speak
// through this interface instead of building bodies, mapping tools, or
// composing stream parsers inline.
type Dialect interface {
	Build(req Request) ([]byte, error)
	// Capabilities reports the generation controls this dialect honors on the wire.
	Capabilities() []GenerationControl
	Manifest(defs []DialectDefinition) any
	Stream(r io.Reader) Stream
}

type ChatCompletionsDialect struct {
	copilotRequestPolicy bool
}

func NewChatCompletionsDialect() *ChatCompletionsDialect {
	return &ChatCompletionsDialect{}
}

func newCopilotChatCompletionsDialect() *ChatCompletionsDialect {
	return &ChatCompletionsDialect{copilotRequestPolicy: true}
}

// chatDialect is the shared stateless dialect used by Chat Completions adapters.
var chatDialect = NewChatCompletionsDialect()

func (d *ChatCompletionsDialect) Build(req Request) ([]byte, error) {
	var promptKey, retention string
	if !d.copilotRequestPolicy {
		promptKey = promptCacheKey(req)
		retention = promptCacheRetention(req)
	}
	body := chatCompletionBody{
		Model:                req.Model,
		Messages:             messagesForWire(req),
		Tools:                toolsForWire(req),
		ToolChoice:           req.ToolChoice,
		Stream:               true,
		StreamOptions:        &streamOptions{IncludeUsage: true},
		PromptCacheKey:       promptKey,
		PromptCacheRetention: retention,
		Thinking:             d.thinkingControl(req),
		ReasoningEffort:      reasoningEffortControl(req),
		MaxOutputTokens:      maxOutputTokens(req),
	}
	return json.Marshal(body)
}

func (d *ChatCompletionsDialect) Capabilities() []GenerationControl {
	controls := []GenerationControl{GenerationControlGenerationBudget}
	if !d.copilotRequestPolicy {
		controls = append(controls, GenerationControlToolSchemaEnforcement)
	}
	return append(controls, GenerationControlThinkingSuppression)
}

func (d *ChatCompletionsDialect) thinkingControl(req Request) *thinkingEnabler {
	if d.copilotRequestPolicy {
		return copilotThinkingControl(req)
	}
	return thinkingControl(req)
}

func (d *ChatCompletionsDialect) Manifest(defs []DialectDefinition) any {
	return chatToolManifest(defs)
}

func (d *ChatCompletionsDialect) Stream(r io.Reader) Stream {
	return &openAIStream{ev: newSSE(r), acc: newToolAccumulator()}
}

type chatCompletionBody struct {
	Model                string           `json:"model"`
	Messages             []Message        `json:"messages"`
	Tools                []Tool           `json:"tools,omitempty"`
	ToolChoice           any              `json:"tool_choice,omitempty"`
	Stream               bool             `json:"stream"`
	StreamOptions        *streamOptions   `json:"stream_options,omitempty"`
	PromptCacheKey       string           `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string           `json:"prompt_cache_retention,omitempty"`
	Thinking             *thinkingEnabler `json:"thinking,omitempty"`
	ReasoningEffort      string           `json:"reasoning_effort,omitempty"`
	MaxOutputTokens      int              `json:"max_completion_tokens,omitempty"`
}

// thinkingEnabler is DeepSeek's thinking-mode toggle; the enabled form keeps thinking default-on for agent loops.
type thinkingEnabler struct {
	Type string `json:"type"`
}

// streamOptions carries the stream_options switch requesting per-turn usage telemetry.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func copilotThinkingControl(req Request) *thinkingEnabler {
	t := "enabled"
	if !req.ThinkingEnabled {
		t = "disabled"
	}
	return &thinkingEnabler{Type: t}
}

// thinkingControl returns the enabled thinking toggle when req opts in, else nil so the field is omitted.
func thinkingControl(req Request) *thinkingEnabler {
	if !req.ThinkingEnabled {
		return nil
	}
	return &thinkingEnabler{Type: "enabled"}
}

// reasoningEffortControl returns the normalized reasoning_effort for a thinking-enabled run, else empty so the field is omitted.
func reasoningEffortControl(req Request) string {
	if !req.ThinkingEnabled {
		return ""
	}
	return NormalizeReasoningEffort(req.ReasoningEffort)
}

func maxOutputTokens(req Request) int {
	if req.MaxOutputTokens <= 0 {
		return 0
	}
	return req.MaxOutputTokens
}

func chatToolManifest(defs []DialectDefinition) []Tool {
	out := make([]Tool, 0, len(defs))
	for _, def := range defs {
		out = append(out, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Schema,
			},
		})
	}
	return out
}

func toolsForWire(req Request) []Tool {
	if len(req.Tools) == 0 {
		return req.Tools
	}
	out := make([]Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		fn := t.Function
		if req.ToolSchemaEnforcement {
			fn.Strict = true
		}
		if !req.Capabilities.Supports(ProviderCapabilityCacheControl) {
			t.CacheControl = nil
		}
		out = append(out, Tool{Type: t.Type, Function: fn, CacheControl: t.CacheControl})
	}
	return out
}

func messagesForWire(req Request) []Message {
	if req.Capabilities.Supports(ProviderCapabilityCacheControl) {
		return req.Messages
	}
	out := make([]Message, len(req.Messages))
	copy(out, req.Messages)
	for i := range out {
		out[i].CacheControl = nil
	}
	return out
}

// prompt-cache retention duration for a Provider that explicitly supports it.
const promptCacheRetention24h = "24h"

func promptCacheKey(req Request) string {
	if req.SetCacheKey && req.Capabilities.Supports(ProviderCapabilityPromptCacheKey) {
		return req.SessionKey
	}
	return ""
}

func promptCacheRetention(req Request) string {
	if !req.Capabilities.Supports(ProviderCapabilityPromptCacheRetention) {
		return ""
	}
	return promptCacheRetention24h
}

// openAIStream adapts parsed SSE events into the Stream seam, mapping [DONE] to a Done chunk and io.EOF to io.EOF, accumulating tool_call fragments.
type openAIStream struct {
	ev  *sse
	acc *toolAccumulator
}

func (os *openAIStream) Next() (Chunk, error) {
	e, err := os.ev.Next()
	if errors.Is(err, io.EOF) {
		return Chunk{}, io.EOF
	}
	if err != nil {
		return Chunk{}, err
	}
	return parseEvent(e.data, os.acc)
}

type wireChunk struct {
	Choices []struct {
		Index *int `json:"index"`
		Delta struct {
			Content          string              `json:"content"`
			Reasoning        string              `json:"reasoning"`
			ReasoningContent string              `json:"reasoning_content"`
			ToolCalls        []wireToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// wireToolCallDelta is one fragmented tool-call delta in a streamed chunk. function.name/arguments arrive split across chunks; the accumulator joins them by index.
type wireToolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolAccumulator reassembles streamed tool_call fragments into complete ToolCalls, keyed by delta index and concatenating each fragment's argument text.
type toolAccumulator struct {
	calls map[int]*ToolCall
	order []int
}

func newToolAccumulator() *toolAccumulator {
	return &toolAccumulator{calls: map[int]*ToolCall{}}
}

func (a *toolAccumulator) add(d wireToolCallDelta) {
	idx := 0
	if d.Index != nil {
		idx = *d.Index
	}
	call, ok := a.calls[idx]
	if !ok {
		call = &ToolCall{}
		a.calls[idx] = call
		a.order = append(a.order, idx)
	}
	if d.ID != "" {
		call.ID = d.ID
	}
	if d.Type != "" {
		call.Type = d.Type
	}
	if d.Function.Name != "" {
		call.Name = d.Function.Name
	}
	call.Arguments += d.Function.Arguments
}

// finish returns the completed ToolCalls, retaining insertion order and only including calls that were actually started (calls that named a function).
func (a *toolAccumulator) finish() []ToolCall {
	var out []ToolCall
	for _, idx := range a.order {
		c := a.calls[idx]
		if c.Name == "" {
			continue
		}
		out = append(out, *c)
	}
	return out
}

func parseEvent(data string, acc *toolAccumulator) (Chunk, error) {
	if data == "[DONE]" {
		if acc != nil {
			return Chunk{Done: true, ToolCalls: acc.finish()}, nil
		}
		return Chunk{Done: true}, nil
	}
	var wc wireChunk
	if err := json.Unmarshal([]byte(data), &wc); err != nil {
		return Chunk{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	chunk := Chunk{}
	if len(wc.Choices) > 0 {
		chunk.Content = wc.Choices[0].Delta.Content
		chunk.ReasoningContent = wc.Choices[0].Delta.ReasoningContent
		if chunk.ReasoningContent == "" {
			chunk.ReasoningContent = wc.Choices[0].Delta.Reasoning
		}
		for _, tc := range wc.Choices[0].Delta.ToolCalls {
			if acc != nil {
				acc.add(tc)
			}
		}
		if wc.Choices[0].FinishReason != nil {
			chunk.FinishReason = *wc.Choices[0].FinishReason
		}
		if acc != nil {
			chunk.ToolCalls = acc.finish()
		}
	}
	chunk.Usage = wc.Usage
	if chunk.Usage != nil {
		chunk.Usage.finalize()
	}
	return chunk, nil
}
