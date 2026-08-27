package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// responsesBody is minimal OpenAI Responses request shape Eitri needs on the Copilot path for models unavailable on /chat/completions.
type responsesBody struct {
	Model           string               `json:"model"`
	Input           []responsesInputItem `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
	ToolChoice      any                  `json:"tool_choice,omitempty"`
	Stream          bool                 `json:"stream"`
	Reasoning       *responsesReasoning  `json:"reasoning,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

type responsesInputItem struct {
	Type      string                 `json:"type,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []responsesContentPart `json:"content,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Output    *string                `json:"output,omitempty"`
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ResponsesDialect is the Dialect implementation for the OpenAI Responses wire.
// It owns the Responses request-body marshaling, canonical-tool mapping, and
// the Responses SSE stream parsing.
type ResponsesDialect struct{}

// NewResponsesDialect returns a Responses dialect.
func NewResponsesDialect() *ResponsesDialect {
	return &ResponsesDialect{}
}

// Build marshals req as the Responses wire request body.
func (d *ResponsesDialect) Build(req Request) ([]byte, error) {
	return marshalResponsesBody(req)
}

// Capabilities reports the generation controls the Responses wire honors.
func (d *ResponsesDialect) Capabilities() []GenerationControl {
	return []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlToolSchemaEnforcement,
		GenerationControlThinkingSuppression,
	}
}

// Manifest re-expresses canonical tool definitions into the Responses tool manifest.
func (d *ResponsesDialect) Manifest(defs []DialectDefinition) any {
	return responsesToolManifest(defs)
}

// Stream returns a stream that parses Responses SSE events.
func (d *ResponsesDialect) Stream(r io.Reader) Stream {
	return newResponsesStream(r)
}

func marshalResponsesBody(req Request) ([]byte, error) {
	body := responsesBody{
		Model:           req.Model,
		Input:           responsesInput(req.Messages),
		Tools:           responsesTools(req),
		ToolChoice:      responsesToolChoice(req.ToolChoice),
		Stream:          true,
		Reasoning:       responsesReasoningControl(req),
		MaxOutputTokens: maxOutputTokens(req),
	}
	return json.Marshal(body)
}

func responsesInput(messages []Message) []responsesInputItem {
	out := make([]responsesInputItem, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem, RoleUser:
			out = append(out, responsesInputItem{
				Role:    string(m.Role),
				Content: responsesTextParts("input_text", m.Content),
			})
		case RoleAssistant:
			if m.Content != "" {
				out = append(out, responsesInputItem{
					Role:    string(m.Role),
					Content: responsesTextParts("output_text", m.Content),
				})
			}
			for _, tc := range m.ToolCalls {
				out = append(out, responsesInputItem{
					Type:      "function_call",
					Name:      tc.Name,
					Arguments: tc.Arguments,
					CallID:    tc.ID,
				})
			}
		case RoleTool:
			output := m.Content
			out = append(out, responsesInputItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: &output,
			})
		}
	}
	return out
}

func responsesTextParts(kind, text string) []responsesContentPart {
	if text == "" {
		return nil
	}
	return []responsesContentPart{{Type: kind, Text: text}}
}

func responsesTools(req Request) []responsesTool {
	tools := toolsForWire(req)
	return responsesToolManifestFromTools(tools)
}

// responsesToolManifest re-expresses canonical tool definitions into the
// Responses tool manifest.
func responsesToolManifest(defs []DialectDefinition) []responsesTool {
	tools := chatToolManifest(defs)
	return responsesToolManifestFromTools(tools)
}

// responsesToolManifestFromTools folds Chat-Completions tools into their
// Responses wire equivalents, returning nil when no tools are present.
func responsesToolManifestFromTools(tools []Tool) []responsesTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responsesTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, responsesTool{
			Type:        t.Type,
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      t.Function.Strict,
		})
	}
	return out
}

func responsesToolChoice(choice any) any {
	m, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	fn, ok := m["function"].(map[string]any)
	if !ok {
		return choice
	}
	name, _ := fn["name"].(string)
	if name == "" {
		return choice
	}
	return map[string]any{"type": "function", "name": name}
}

func responsesReasoningControl(req Request) *responsesReasoning {
	if !req.ThinkingEnabled {
		return nil
	}
	effort := NormalizeReasoningEffort(req.ReasoningEffort)
	if effort == "" {
		return nil
	}
	return &responsesReasoning{Effort: effort}
}

// responsesStream adapts OpenAI Responses SSE events into Eitri's provider stream seam.
type responsesStream struct {
	ev        *sse
	acc       *responsesToolAccumulator
	sawText   bool
	sawReason bool
}

func newResponsesStream(r io.Reader) *responsesStream {
	return &responsesStream{ev: newSSE(r), acc: newResponsesToolAccumulator()}
}

func (rs *responsesStream) Next() (Chunk, error) {
	e, err := rs.ev.Next()
	if err != nil {
		return Chunk{}, err
	}
	return parseResponsesEvent(e.data, rs)
}

type responsesToolAccumulator struct {
	calls map[int]*ToolCall
	order []int
}

func newResponsesToolAccumulator() *responsesToolAccumulator {
	return &responsesToolAccumulator{calls: map[int]*ToolCall{}}
}

func (a *responsesToolAccumulator) ensure(idx int) *ToolCall {
	call, ok := a.calls[idx]
	if !ok {
		call = &ToolCall{Type: "function"}
		a.calls[idx] = call
		a.order = append(a.order, idx)
	}
	return call
}

func (a *responsesToolAccumulator) start(idx int, callID, name string) {
	call := a.ensure(idx)
	if callID != "" {
		call.ID = callID
	}
	if name != "" {
		call.Name = name
	}
}

func (a *responsesToolAccumulator) addArgs(idx int, delta string) {
	a.ensure(idx).Arguments += delta
}

func (a *responsesToolAccumulator) done(idx int, callID, name, args string) {
	call := a.ensure(idx)
	if callID != "" {
		call.ID = callID
	}
	if name != "" {
		call.Name = name
	}
	if args != "" {
		call.Arguments = args
	}
}

func (a *responsesToolAccumulator) finish() []ToolCall {
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

type responsesEventEnvelope struct {
	Type        string              `json:"type"`
	Delta       string              `json:"delta"`
	OutputIndex int                 `json:"output_index"`
	Item        json.RawMessage     `json:"item"`
	Response    responsesCompletion `json:"response"`
}

type responsesOutputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Summary   []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type responsesCompletion struct {
	ID        string                `json:"id"`
	Model     string                `json:"model"`
	CreatedAt int64                 `json:"created_at"`
	Usage     *responsesUsage       `json:"usage"`
	Output    []responsesOutputItem `json:"output"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	InputDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func parseResponsesEvent(data string, rs *responsesStream) (Chunk, error) {
	if data == "[DONE]" {
		return Chunk{Done: true, ToolCalls: rs.acc.finish()}, nil
	}
	var env responsesEventEnvelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return Chunk{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	switch env.Type {
	case "error":
		var body map[string]any
		_ = json.Unmarshal([]byte(data), &body)
		encoded, _ := json.Marshal(body)
		return Chunk{}, &HTTPError{Code: 400, Body: string(encoded)}

	case "response.output_text.delta":
		rs.sawText = true
		return Chunk{Content: env.Delta}, nil

	case "response.reasoning_summary_text.delta":
		rs.sawReason = true
		return Chunk{ReasoningContent: env.Delta}, nil

	case "response.output_item.added":
		var item responsesOutputItem
		if err := json.Unmarshal(env.Item, &item); err != nil {
			return Chunk{}, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if item.Type == "function_call" {
			rs.acc.start(env.OutputIndex, item.CallID, item.Name)
		}
		return Chunk{}, nil

	case "response.function_call_arguments.delta":
		rs.acc.addArgs(env.OutputIndex, env.Delta)
		return Chunk{}, nil

	case "response.output_item.done":
		var item responsesOutputItem
		if err := json.Unmarshal(env.Item, &item); err != nil {
			return Chunk{}, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if item.Type == "function_call" {
			rs.acc.done(env.OutputIndex, item.CallID, item.Name, item.Arguments)
			return Chunk{}, nil
		}
		if item.Type == "reasoning" && !rs.sawReason {
			parts := make([]string, 0, len(item.Summary))
			for _, s := range item.Summary {
				if s.Text != "" {
					parts = append(parts, s.Text)
				}
			}
			if len(parts) > 0 {
				rs.sawReason = true
				return Chunk{ReasoningContent: strings.Join(parts, "")}, nil
			}
		}
		return Chunk{}, nil

	case "response.completed":
		toolCalls := rs.acc.finish()
		chunk := Chunk{
			Done:         true,
			ToolCalls:    toolCalls,
			FinishReason: "stop",
		}
		if len(toolCalls) > 0 {
			chunk.FinishReason = "tool_calls"
		}
		if env.Response.Usage != nil {
			chunk.Usage = &Usage{
				PromptTokens:     env.Response.Usage.InputTokens,
				CompletionTokens: env.Response.Usage.OutputTokens,
			}
			if env.Response.Usage.InputDetails != nil {
				chunk.Usage.PromptCacheHitTokens = env.Response.Usage.InputDetails.CachedTokens
				chunk.Usage.cacheHitAssigned = true
			}
			chunk.Usage.finalize()
		}
		if !rs.sawText && len(toolCalls) == 0 {
			chunk.Content = responsesCompletedText(env.Response.Output)
		}
		return chunk, nil
	}
	return Chunk{}, nil
}

func responsesCompletedText(items []responsesOutputItem) string {
	var buf bytes.Buffer
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			switch c.Type {
			case "output_text":
				buf.WriteString(c.Text)
			case "refusal":
				buf.WriteString(c.Refusal)
			}
		}
	}
	return buf.String()
}
