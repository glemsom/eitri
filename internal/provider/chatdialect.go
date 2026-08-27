package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Dialect is the seam for a single wire dialect: it owns both request shaping
// (building the wire body with the generation-control fields), tool-mapping
// (re-expressing canonical tool definitions into the dialect's tool manifest),
// and wire parsing (decoding the SSE stream back into chunks). Adapters speak
// through this interface instead of building bodies, mapping tools, or
// composing stream parsers inline.
type Dialect interface {
	// Build shapes req into the dialect's serialized wire request body.
	Build(req Request) ([]byte, error)
	// Capabilities reports the generation controls this dialect honors on the wire.
	Capabilities() []GenerationControl
	// Manifest re-expresses canonical tool definitions into this dialect's tool wire form.
	Manifest(defs []DialectDefinition) any
	// Stream wraps an SSE body stream, parsing it into provider chunks.
	Stream(r io.Reader) Stream
}

// ChatCompletionsDialect is the Dialect implementation for the OpenAI
// Chat-Completions wire: it builds the /chat/completions request body, maps
// canonical tools to its function manifest, and reassembles streamed tool-call
// fragments.
type ChatCompletionsDialect struct{}

// NewChatCompletionsDialect returns a Chat-Completions dialect.
func NewChatCompletionsDialect() *ChatCompletionsDialect {
	return &ChatCompletionsDialect{}
}

// chatDialect is the package-wide Chat-Completions dialect the adapters speak through.
// It is stateless, so the single shared instance is safe.
var chatDialect = NewChatCompletionsDialect()

// Build implements Dialect.
func (d *ChatCompletionsDialect) Build(req Request) ([]byte, error) {
	body := chatCompletionBody{
		Model:                req.Model,
		Messages:             stampCacheBreakpoints(req),
		Tools:                toolsForWire(req),
		ToolChoice:           req.ToolChoice,
		Stream:               true,
		StreamOptions:        &streamOptions{IncludeUsage: true}, // opencode force-sets include_usage
		PromptCacheKey:       promptCacheKey(req),
		PromptCacheRetention: promptCacheRetention(req),
		Thinking:             thinkingControl(req),
		ReasoningEffort:      reasoningEffortControl(req),
		MaxOutputTokens:      maxOutputTokens(req),
		ResponseFormat:       jsonObjectModeControl(req),
		Temperature:          samplingTemperatureControl(req),
		TopP:                 samplingTopPControl(req),
	}
	return json.Marshal(body)
}

// Capabilities implements Dialect.
func (d *ChatCompletionsDialect) Capabilities() []GenerationControl {
	return []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlJSONObjectMode,
		GenerationControlSamplingPolicy,
		GenerationControlToolSchemaEnforcement,
		GenerationControlThinkingSuppression,
	}
}

// Manifest implements Dialect, re-expressing canonical tool definitions into
// the Chat-Completions function manifest.
func (d *ChatCompletionsDialect) Manifest(defs []DialectDefinition) any {
	return chatToolManifest(defs)
}

// Stream implements Dialect, returning a stream that parses Chat-Completions
// SSE events and folds streamed tool_call fragments into complete calls.
func (d *ChatCompletionsDialect) Stream(r io.Reader) Stream {
	return &openAIStream{ev: newSSE(r), acc: newToolAccumulator()}
}

// chatCompletionBody is the OpenAI Chat-Completions request shape.
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
	ResponseFormat       *jsonObjectMode  `json:"response_format,omitempty"`
	Temperature          *float64         `json:"temperature,omitempty"`
	TopP                 *float64         `json:"top_p,omitempty"`
}

// thinkingEnabler is DeepSeek's thinking-mode toggle; the enabled form keeps thinking default-on for agent loops.
type thinkingEnabler struct {
	Type string `json:"type"`
}

// streamOptions carries the stream_options switch requesting per-turn usage telemetry.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
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

// jsonObjectMode is OpenAI's constrained-output response_format; its enabled form asks the provider to return a valid JSON object.
type jsonObjectMode struct {
	Type string `json:"type"`
}

// maxOutputTokens returns the Generation Budget for req as an int usable as a wire max_completion_tokens: it is 0 when no budget was requested so the field is omitted.
func maxOutputTokens(req Request) int {
	if req.MaxOutputTokens <= 0 {
		return 0
	}
	return req.MaxOutputTokens
}

// jsonObjectModeControl returns the JSON Object Mode response_format for req when the caller opted into it, else nil so the field is omitted.
func jsonObjectModeControl(req Request) *jsonObjectMode {
	if !req.JSONObjectMode {
		return nil
	}
	return &jsonObjectMode{Type: "json_object"}
}

// samplingTemperatureControl returns the pointer to emit as the wire `temperature` when req requests temperature-based sampling, else nil so the field is omitted.
func samplingTemperatureControl(req Request) *float64 {
	if req.Sampling == nil || req.Sampling.Mode != SamplingTemperature {
		return nil
	}
	v := req.Sampling.Value
	return &v
}

// samplingTopPControl returns the pointer to emit as the wire `top_p` when req requests nucleus (top-p) sampling, else nil so the field is omitted.
func samplingTopPControl(req Request) *float64 {
	if req.Sampling == nil || req.Sampling.Mode != SamplingNucleus {
		return nil
	}
	v := req.Sampling.Value
	return &v
}

// chatToolManifest re-expresses canonical tool definitions into the
// Chat-Completions function manifest.
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

// toolsForWire returns the tool manifest to serialize for req.
func toolsForWire(req Request) []Tool {
	if !req.ToolSchemaEnforcement || len(req.Tools) == 0 {
		return req.Tools
	}
	out := make([]Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		fn := t.Function
		fn.Strict = true
		out = append(out, Tool{Type: t.Type, Function: fn, CacheControl: t.CacheControl})
	}
	return out
}

// promptCacheRetention returns the OpenCode Go prompt-cache retention duration so the gateway keeps the session cache alive for a day, else empty so the field is omitted and unrelated endpoints stay untouched.
const promptCacheRetention24h = "24h"

func promptCacheRetention(req Request) string {
	if req.ProviderID != ProviderOpenCodeGo {
		return ""
	}
	return promptCacheRetention24h
}

// promptCacheKey returns the session-scoped prompt cache key for req when the caller opted into deepseek's session cache, else empty so the field is omitted from the body.
func promptCacheKey(req Request) string {
	if req.SetCacheKey {
		return req.SessionKey
	}
	return ""
}

// cacheMarker is the Anthropic-style breakpoint stamped on OpenCode Go turns so
// long sessions stay cheap: the stable system prefix and earlier turns keep
// hitting the cache while the newest message changes every turn.
var cacheMarker = &CacheControl{Type: "ephemeral", TTL: "1h"}

// stampCacheBreakpoints returns req.Messages with OpenCode Go cache breakpoints
// stamped on up to two leading system messages, the last user/assistant message
// pair, and the last tool message, on a fresh copy so the caller's slice is
// untouched. It returns the slice unchanged when OpenCode Go stamping does not
// apply: custom-openai turns, GLM/Zhipu models (whose API rejects Anthropic-style
// markers), or a request that already carries a marker anywhere (no double-stamp).
func stampCacheBreakpoints(req Request) []Message {
	if req.ProviderID != ProviderOpenCodeGo || isGLMModel(req.Model) || carriesCacheMarker(req) {
		return req.Messages
	}
	out := make([]Message, len(req.Messages))
	copy(out, req.Messages)

	systems := 0
	for i := range out {
		if out[i].Role != RoleSystem || systems >= 2 {
			break
		}
		out[i].CacheControl = cacheMarker
		systems++
	}

	// moving tail: last tool message plus the last two user/assistant messages
	lastTool := -1
	lastTwoTurns := []int{}
	for i := len(out) - 1; i >= 0; i-- {
		switch out[i].Role {
		case RoleTool:
			if lastTool < 0 {
				lastTool = i
			}
		case RoleUser, RoleAssistant:
			if len(lastTwoTurns) < 2 {
				lastTwoTurns = append([]int{i}, lastTwoTurns...)
			}
		}
	}
	if lastTool >= 0 {
		out[lastTool].CacheControl = cacheMarker
	}
	for _, i := range lastTwoTurns {
		out[i].CacheControl = cacheMarker
	}
	return out
}

// isGLMModel reports whether req.Model is a Zhipu GLM variant, whose downstream
// API rejects Anthropic-style cache_control markers.
func isGLMModel(model string) bool {
	return strings.Contains(model, "glm-") || strings.Contains(model, "zhipu-")
}

// carriesCacheMarker reports whether any message or tool already carries a cache_control
// marker, which forbids the dialect from stamping another one.
func carriesCacheMarker(req Request) bool {
	for i := range req.Messages {
		if req.Messages[i].CacheControl != nil {
			return true
		}
	}
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			return true
		}
	}
	return false
}

// openAIStream adapts parsed SSE events into the Stream seam, mapping [DONE] to a Done chunk and io.EOF to io.EOF, accumulating tool_call fragments.
type openAIStream struct {
	ev  *sse
	acc *toolAccumulator
}

// Next implements Stream.
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

// wireChunk is the OpenAI Chat-Completions `chat.completion.chunk` SSE payload.
type wireChunk struct {
	Choices []struct {
		Index *int `json:"index"`
		Delta struct {
			Content          string              `json:"content"`
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

// add folds one fragmented delta into the per-index ToolCall.
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

// parseEvent turns one SSE data payload into a Chunk, folding streamed tool_call fragments into acc.
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
		for _, tc := range wc.Choices[0].Delta.ToolCalls {
			if acc != nil {
				acc.add(tc)
			}
		}
		if wc.Choices[0].FinishReason != nil {
			chunk.FinishReason = *wc.Choices[0].FinishReason
		}
		chunk.ToolCalls = accTouls(acc)
	}
	chunk.Usage = wc.Usage
	if chunk.Usage != nil {
		chunk.Usage.finalize()
	}
	return chunk, nil
}

// accTouls returns the accumulator's current finished ToolCalls (reflects the running reassembly on each non-terminal chunk).
func accTouls(acc *toolAccumulator) []ToolCall {
	if acc == nil {
		return nil
	}
	return acc.finish()
}
