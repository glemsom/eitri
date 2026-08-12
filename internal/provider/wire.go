package provider

import (
	"encoding/json"
	"fmt"
)

// wireChunk is the OpenAI Chat-Completions `chat.completion.chunk` SSE payload.
// Only the fields Eitri consumes are modeled; everything else is ignored so an
// unknown wire piece still parses (docs/spec.md §11/§12).
type wireChunk struct {
	Choices []struct {
		Index *int `json:"index"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// parseEvent turns one SSE data payload into a Chunk. The terminal "[DONE]"
// marker yields a Done chunk; any other payload must be a valid chunk object,
// else ErrMalformed is returned (never a panic).
func parseEvent(data string) (Chunk, error) {
	if data == "[DONE]" {
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
	}
	chunk.Usage = wc.Usage
	return chunk, nil
}
