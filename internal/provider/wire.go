package provider

import (
	"encoding/json"
	"fmt"
)

// wireChunk is the OpenAI Chat-Completions `chat.completion.chunk` SSE payload.
// Only the fields Eitri consumes are modeled; everything else is ignored so an
// unknown wire piece still parses.
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

// wireToolCallDelta is one fragmented tool-call delta in a streamed chunk.
// function.name/arguments arrive split across chunks; the accumulator joins
// them by index.
type wireToolCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolAccumulator reassembles streamed tool_call fragments into complete
// ToolCalls, keyed by delta index and concatenating each fragment's argument
// text (research/tool-exposure.md §3).
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

// finish returns the completed ToolCalls, retaining insertion order and only
// including calls that were actually started (calls that named a function).
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

// parseEvent turns one SSE data payload into a Chunk, folding streamed
// tool_call fragments into acc. The terminal "[DONE]" marker yields a Done
// chunk carrying the finished ToolCalls; any other payload must be a valid
// chunk object, else ErrMalformed is returned (never a panic).
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
		// Apply the absent-key safe-parse fallback so an OpenCode proxy that
		// omits prompt_cache_* still produces honest hit/miss telemetry (issue
		// #218): never a fake hit, never a mispriced bill.
		chunk.Usage.finalize()
	}
	return chunk, nil
}

// accTouls returns the accumulator's current finished ToolCalls (reflects the
// running reassembly on each non-terminal chunk).
func accTouls(acc *toolAccumulator) []ToolCall {
	if acc == nil {
		return nil
	}
	return acc.finish()
}
