package debug

import (
	"bytes"
	"encoding/json"
)

// maxTailBytes is the size of the rolling tail window kept alongside the
// truncated head of a response body. Streaming responses carry usage and
// finish_reason in the final SSE chunks; without the tail window those would
// be lost when the body exceeds MaxBodyBytes.
const maxTailBytes = 64 * 1024

// headBytes returns the bytes the recorder keeps as a trace's response body:
// the first MaxBodyBytes bytes, with the truncation suffix appended when the
// original exceeds the cap.
func headBytes(body []byte) []byte {
	if len(body) <= MaxBodyBytes {
		return truncateBody(body)
	}
	return truncateBody(body[:MaxBodyBytes])
}

// tailBytes returns the rolling tail window of a response body — the last
// maxTailBytes bytes — used to recover the stream tail (usage and
// finish_reason chunks) when the head is truncated at MaxBodyBytes. It
// returns nil for bodies within the cap.
func tailBytes(body []byte) []byte {
	if len(body) <= MaxBodyBytes {
		return nil
	}
	if len(body) <= maxTailBytes {
		return body
	}
	return body[len(body)-maxTailBytes:]
}

// rawUsage mirrors the union of provider usage payloads: OpenAI-compatible
// (prompt_tokens/completion_tokens/total_tokens + prompt_tokens_details.
// cached_tokens) and Anthropic (input_tokens/output_tokens/cache_read_input_
// tokens/cache_creation_input_tokens).
type rawUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// parseResponseEnrichment extracts provider-reported usage totals, finish
// reason, and model name from an LLM response body. It accepts both single
// JSON payloads (non-streaming) and SSE streams (data: lines). In SSE
// streams the enrichment is merged across chunks, so the input_tokens from a
// message_start chunk and the output_tokens from a later chunk combine into
// one UsageTotals.
func parseResponseEnrichment(body []byte) (usage *UsageTotals, finishReason, model string) {
	var (
		foundDataLine bool
		pending       []byte // holds the whole body when no data: line was found
	)

	apply := func(data []byte) {
		chunk := parseChunk(data)
		if chunk.model != "" {
			model = chunk.model
		}
		if chunk.finishReason != "" {
			finishReason = chunk.finishReason
		}
		if chunk.usage != nil {
			usage = mergeUsage(usage, chunk.usage)
		}
	}

	for _, line := range bytes.Split(body, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("data:")) {
			continue
		}
		foundDataLine = true
		data := bytes.TrimSpace(trimmed[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		apply(data)
	}

	if !foundDataLine {
		pending = bytes.TrimSpace(body)
		if len(pending) > 0 {
			apply(pending)
		}
	}

	if usage != nil && usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens +
			usage.CacheReadTokens + usage.CacheWriteTokens
	}
	return usage, finishReason, model
}

// chunk is the subset of a provider response (or SSE data payload) that
// contributes to enrichment.
type chunk struct {
	model        string
	finishReason string
	usage        *UsageTotals
}

func parseChunk(data []byte) chunk {
	var payload struct {
		Model   string    `json:"model"`
		Type    string    `json:"type"`
		Usage   *rawUsage `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		StopReason string `json:"stop_reason"`
		Message    *struct {
			Model string    `json:"model"`
			Usage *rawUsage `json:"usage"`
		} `json:"message"`
		Delta *struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return chunk{}
	}

	c := chunk{model: payload.Model}
	if c.model == "" && payload.Message != nil {
		c.model = payload.Message.Model
	}
	if payload.StopReason != "" {
		c.finishReason = payload.StopReason
	}
	if len(payload.Choices) > 0 && payload.Choices[0].FinishReason != "" {
		c.finishReason = payload.Choices[0].FinishReason
	}
	if payload.Delta != nil && payload.Delta.StopReason != "" {
		c.finishReason = payload.Delta.StopReason
	}
	if u := payload.Usage; u != nil {
		c.usage = fromRawUsage(u)
	}
	if payload.Message != nil && payload.Message.Usage != nil {
		c.usage = mergeUsage(c.usage, fromRawUsage(payload.Message.Usage))
	}
	return c
}

// fromRawUsage converts a raw provider usage payload to UsageTotals. The
// Anthropic input/output token names map onto prompt/completion; cache fields
// map onto cache read/write. TotalTokens is only copied when the provider
// reports it explicitly (the final fallback is applied by the caller).
func fromRawUsage(u *rawUsage) *UsageTotals {
	out := &UsageTotals{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
	}
	if u.InputTokens > 0 {
		out.PromptTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		out.CompletionTokens = u.OutputTokens
	}
	if u.CacheReadInputTokens > 0 {
		out.CacheReadTokens = u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		out.CacheWriteTokens = u.CacheCreationInputTokens
	}
	return out
}

// mergeUsage unions two UsageTotals collected from different SSE chunks. Each
// field takes the last non-zero value; TotalTokens takes the maximum explicit
// value.
func mergeUsage(dst, src *UsageTotals) *UsageTotals {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = &UsageTotals{}
	}
	lastNonZero := func(dst *int, v int) {
		if v > 0 {
			*dst = v
		}
	}
	lastNonZero(&dst.PromptTokens, src.PromptTokens)
	lastNonZero(&dst.CompletionTokens, src.CompletionTokens)
	lastNonZero(&dst.CacheReadTokens, src.CacheReadTokens)
	lastNonZero(&dst.CacheWriteTokens, src.CacheWriteTokens)
	if src.TotalTokens > dst.TotalTokens {
		dst.TotalTokens = src.TotalTokens
	}
	return dst
}
