// Package compactor compresses tool-result messages in conversation history
// by replacing them with LLM-generated summaries.
package compactor

import (
	"context"
	"fmt"
	"strings"

	"github.com/glemsom/eitri/internal/llm"
)

// Thresholds controls when compaction triggers and stops.
// HighWater and LowWater are absolute estimated-token counts.
type Thresholds struct {
	// HighWater is the estimated-token count above which compaction
	// is triggered (e.g. 90% of the context window).
	HighWater int

	// LowWater is the estimated-token count below which compaction
	// stops (e.g. 30% of the context window).
	LowWater int
}

// tokenEstimate returns a rough estimate of the number of tokens in s.
// Uses len(s)/4 as a simple heuristic (≈4 chars per token for typical text).
func tokenEstimate(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}

// messagesTokenEstimate returns the sum of estimated tokens across all messages.
func messagesTokenEstimate(msgs []llm.Message) int {
	var total int
	for _, m := range msgs {
		total += tokenEstimate(m.Content)
		// Also account for tool call payloads
		for _, tc := range m.ToolCalls {
			total += tokenEstimate(tc.Function.Name)
			total += tokenEstimate(tc.Function.Arguments)
		}
	}
	return total
}

// MessagesTokenEstimate is the public equivalent of messagesTokenEstimate.
// It estimates the total token count for a slice of messages using the same 4-char-per-token heuristic.
func MessagesTokenEstimate(msgs []llm.Message) int {
	return messagesTokenEstimate(msgs)
}

// Compactor compresses tool-result messages in conversation history
// by replacing them with LLM-generated summaries.
type Compactor struct{}

// New creates a new Compactor.
func New() *Compactor {
	return &Compactor{}
}

// summarizationPrompt builds a prompt that asks the LLM to summarise a
// tool-result payload, preserving key facts.
func summarizationPrompt(content string) string {
	const maxContentLen = 8000
	truncated := content
	if len(truncated) > maxContentLen {
		truncated = truncated[:maxContentLen] + "\n... [truncated]"
	}
	return fmt.Sprintf(`Summarize the following tool result in 1-3 sentences. Preserve key facts, file paths, error messages, function names, and numerical values. Omit boilerplate and verbose logs.

Tool result content:
%s

Summary:`, truncated)
}

// Compact scans the conversation history and replaces tool-result messages
// with LLM-generated summaries. It always attempts compaction regardless of
// the estimated token count — the caller decides whether to call Compact
// (e.g. auto-compaction gates in run.go).
//
// Compaction stops once the total falls below the LowWater threshold.
// The LowWater stop condition prevents runaway compaction of the entire history.
//
// The scan proceeds greedily from the oldest tool result forward.
// If a summarization call fails for one tool result, it is skipped and
// compaction continues with the next one.
//
// Returns the modified message list (a copy) if any compaction occurred,
// or nil if no compaction was needed. The original slice is never modified.
// compactedCount and freedTokens report the number of messages compacted
// and the approximate token count saved.
func (c *Compactor) Compact(ctx context.Context, messages []llm.Message, llmSvc llm.LLMService, thresholds Thresholds) (compacted []llm.Message, compactedCount int, freedTokens int, err error) {
	if thresholds.HighWater <= 0 {
		thresholds.HighWater = 90 // sensible default
	}
	if thresholds.LowWater <= 0 {
		thresholds.LowWater = 30
	}
	if thresholds.LowWater >= thresholds.HighWater {
		thresholds.LowWater = thresholds.HighWater / 3
	}

	totalEst := messagesTokenEstimate(messages)

	// Work on a copy so the original is never mutated.
	result := make([]llm.Message, len(messages))
	copy(result, messages)

	compactedCount = 0
	freedTokens = 0

	// Greedy oldest-first scan: iterate from oldest to newest.
	for i := 0; i < len(result); i++ {
		if result[i].Role != "tool" {
			continue
		}
		if result[i].Content == "" {
			continue
		}

		// Skip already-compacted messages.
		if strings.HasPrefix(result[i].Content, "[TOOL RESULT COMPACTED") {
			continue
		}

		originalContent := result[i].Content
		originalTokens := tokenEstimate(originalContent)

		// Call LLM to summarise.
		prompt := summarizationPrompt(originalContent)
		summaries, err := llmSvc.Chat(ctx, llm.Request{
			Messages: []llm.Message{
				{Role: "user", Content: prompt},
			},
		})
		if err != nil {
			// Skip this one, continue with the next.
			continue
		}

		summary := strings.TrimSpace(summaries.Content)
		if summary == "" {
			continue
		}

		result[i].Content = fmt.Sprintf("[TOOL RESULT COMPACTED - originally %d tokens] %s", originalTokens, summary)
		compactedCount++
		freedTokens += originalTokens - tokenEstimate(result[i].Content)

		// Re-estimate total; stop if below LowWater.
		totalEst = messagesTokenEstimate(result)
		if totalEst <= thresholds.LowWater {
			break
		}
	}

	if compactedCount == 0 {
		return nil, 0, 0, nil
	}

	return result, compactedCount, freedTokens, nil
}
