// Package tokenizer provides token estimation and calibration utilities.
package tokenizer

import (
	"strings"

	"github.com/glemsom/eitri/internal/message"
)

// TokenUsage holds token count information for a completed run.
type TokenUsage struct {
	TotalTokens      int `json:"total_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ContextUpdate holds estimated token counts broken down by category.
type ContextUpdate struct {
	TotalTokens      int `json:"total_tokens"`
	ContextWindow    int `json:"context_window"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	SystemTokens     int `json:"system_tokens"`
	HistoryTokens    int `json:"history_tokens"`
	SkillTokens      int `json:"skill_tokens"`
	// Actual provider token usage from the LLM response (if available).
	ActualPromptTokens     int `json:"actual_prompt_tokens,omitempty"`
	ActualCompletionTokens int `json:"actual_completion_tokens,omitempty"`
}

// Estimate returns a rough estimate of the number of tokens in text using the
// CalibrationStore's chars-per-token ratio for the model, falling back to
// DefaultCPT (4.0) when store is nil. This is the single canonical estimator —
// every token count in the system routes through it. It runs on the
// allocation-free hot path (bare int return) so compactor/compress call sites
// can call it without allocation.
func Estimate(text string, store *CalibrationStore, model string) int {
	return estimateLen(len(text), store, model)
}

// estimateLen estimates tokens for a content length using the chars-per-token
// ratio from store (or DefaultCPT when nil). It floors at 1 for non-empty
// content, and returns 0 for empty content.
func estimateLen(length int, store *CalibrationStore, model string) int {
	if length <= 0 {
		return 0
	}
	cpt := DefaultCPT
	if store != nil {
		cpt = store.Lookup(model)
	}
	n := int(float64(length) / cpt)
	if n < 1 {
		return 1
	}
	return n
}

// EstimateUsage estimates token counts from text length.
// Uses the CalibrationStore's chars-per-token ratio for the given model,
// falling back to 4.0 (the default) when store is nil.
// This is a fallback when the provider doesn't return usage data.
func EstimateUsage(text string, store *CalibrationStore, model string) *TokenUsage {
	totalTokens := Estimate(text, store, model)
	// The bare primitive reports 0 for empty text; the usage breakdown always
	// reports at least one total token so an empty run still carries a usable
	// fallback figure (matches the pre-consolidation EstimateUsage behavior).
	if totalTokens < 1 {
		totalTokens = 1
	}
	// Rough split: ~2/3 prompt tokens, ~1/3 completion tokens
	completionTokens := totalTokens / 3
	if completionTokens < 1 {
		completionTokens = 1
	}
	promptTokens := totalTokens - completionTokens
	return &TokenUsage{
		TotalTokens:      totalTokens,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}
}

// ComputeContext estimates token counts for the given messages using a
// configurable chars-per-token ratio (from CalibrationStore, default 4.0).
//
// Token breakdown:
//   - System tokens: sum of content lengths for messages with role "system"
//   - Skill tokens: portion of system prompt after the last "Activated skill" substring
//   - History tokens: sum of all non-system messages (user + assistant + tool)
//   - Prompt tokens: system + history (skill tokens are part of system)
//   - Completion tokens: 0 (set by caller when known)
//   - Total tokens: prompt + completion
func ComputeContext(messages []message.Message, contextWindow int, store *CalibrationStore, model string) *ContextUpdate {
	var historyLen int
	var systemBuilder strings.Builder

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemBuilder.WriteString(msg.Content)
		default:
			historyLen += len(msg.Content)
		}
	}

	systemContent := systemBuilder.String()
	systemTokens := Estimate(systemContent, store, model)

	// Skill tokens: content after last "Activated skill" in system prompt
	var skillTokens int
	if idx := strings.LastIndex(systemContent, "Activated skill"); idx >= 0 {
		skillContent := systemContent[idx+len("Activated skill"):]
		skillTokens = Estimate(skillContent, store, model)
	}

	historyTokens := estimateLen(historyLen, store, model)

	promptTokens := systemTokens + historyTokens
	completionTokens := 0
	totalTokens := promptTokens + completionTokens

	return &ContextUpdate{
		TotalTokens:      totalTokens,
		ContextWindow:    contextWindow,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		SystemTokens:     systemTokens,
		HistoryTokens:    historyTokens,
		SkillTokens:      skillTokens,
	}
}
