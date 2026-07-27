// Package compactor compresses tool-result messages in conversation history
// by replacing them with LLM-generated summaries.
package compactor

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/tokenizer"
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

	// MessageSizeThreshold is the minimum estimated-token count for an individual
	// message (of any role) to be considered for compaction. Messages below this
	// threshold are skipped. Default 0 means any message is eligible (existing
	// behaviour for tool messages). Suggested default: 2000 tokens.
	MessageSizeThreshold int

	// ToolCallRetentionTurns controls how many recent assistant messages retain
	// their full ToolCall arguments. Assistant messages older than this (counting
	// only assistant messages) have their ToolCall.Function.Arguments pruned to a
	// compact placeholder. Default 0 means no pruning.
	ToolCallRetentionTurns int

	// SalienceEnabled controls whether salience-scored compaction ordering is used.
	// When enabled, the compactor scores each compactable message by heuristic
	// importance and compacts the lowest-scoring (least important) messages first.
	// When disabled, the original oldest-first greedy behaviour is used. Default true.
	SalienceEnabled bool

	// HighSalienceSkipThreshold is the salience score above which a message is
	// skipped entirely during compaction (not considered for compaction even if
	// it exceeds the size threshold). A value of 0 means no messages are skipped
	// based on salience (only the scoring order is used). Default 0.
	HighSalienceSkipThreshold int
}

// tokenEstimate returns a rough estimate of the number of tokens in s.
// Uses the CalibrationStore's chars-per-token ratio for the given model,
// falling back to 4.0 (the default) when store is nil.
func tokenEstimate(s string, store *tokenizer.CalibrationStore, model string) int {
	if len(s) == 0 {
		return 0
	}
	cpt := 4.0
	if store != nil {
		cpt = store.Lookup(model)
	}
	n := int(float64(len(s)) / cpt)
	if n < 1 {
		return 1
	}
	return n
}

// messagesTokenEstimate returns the sum of estimated tokens across all messages.
func messagesTokenEstimate(msgs []message.Message, store *tokenizer.CalibrationStore, model string) int {
	var total int
	for _, m := range msgs {
		total += tokenEstimate(m.Content, store, model)
		// Also account for tool call payloads
		for _, tc := range m.ToolCalls {
			total += tokenEstimate(tc.Function.Name, store, model)
			total += tokenEstimate(tc.Function.Arguments, store, model)
		}
	}
	return total
}

// MessagesTokenEstimate is the public equivalent of messagesTokenEstimate.
// It estimates the total token count for a slice of messages using the same chars-per-token heuristic.
func MessagesTokenEstimate(msgs []message.Message, store *tokenizer.CalibrationStore, model string) int {
	return messagesTokenEstimate(msgs, store, model)
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
func summarizationPrompt(role, content string) string {
	const maxContentLen = 8000
	truncated := content
	if len(truncated) > maxContentLen {
		truncated = truncated[:maxContentLen] + "\n... [truncated]"
	}

	switch role {
	case "user":
		return fmt.Sprintf(`Summarize the user's request in 1-3 sentences. Preserve key questions, file paths, error messages, and any specific requirements. Omit boilerplate and verbose logs.

User message content:
%s

Summary:`, truncated)
	case "assistant":
		return fmt.Sprintf(`Summarize the assistant's response in 1-3 sentences. Preserve key facts, file paths, error messages, function names, and numerical values. Omit boilerplate and verbose reasoning.

Assistant message content:
%s

Summary:`, truncated)
	default:
		return fmt.Sprintf(`Summarize the following tool result in 1-3 sentences. Preserve key facts, file paths, error messages, function names, and numerical values. Omit boilerplate and verbose logs.

Tool result content:
%s

Summary:`, truncated)
	}
}

// salienceScore returns a heuristic importance score for a message's content.
// Higher scores indicate more important content that should be preserved longer.
// The score considers:
//   - Presence of error/failure indicators (+20 each)
//   - Presence of stack traces (+30)
//   - Presence of file paths and function names (+10 each)
//   - Presence of numerical results/measurements (+5 each)
//   - Message length: very short messages (<100 chars) score low (-15)
//   - Message length: very long verbose messages (>5000 chars) score lower (-10)
//   - Absence of any signal above means plain boilerplate scores lowest
func salienceScore(content string) int {
	if content == "" {
		return 0
	}
	score := 50 // base score for any content

	// Error/failure indicators
	errorPatterns := []string{
		"(?i)\\berror\\b",
		"(?i)\\bfail(ed|ure)?\\b",
		"(?i)\\bexception\\b",
		"(?i)\\bpanic\\b",
		"(?i)\\bcrash\\b",
		"(?i)\\b(sigsegv|segfault|core dump)\\b",
		"(?i)\\b(timeout|timed out)\\b",
		"(?i)\\bpermission denied\\b",
		"(?i)\\bnot found\\b",
		"(?i)\\bcannot find\\b",
		"(?i)\\bunable to\\b",
		"(?i)\\bundefined\\b",
		"(?i)\\bsyntax error\\b",
		"(?i)\\bcompile error\\b",
	}
	for _, pat := range errorPatterns {
		if matched, _ := regexp.MatchString(pat, content); matched {
			score += 20
		}
	}

	// Stack traces (lines starting with spaces and containing file paths)
	stackTracePattern := regexp.MustCompile(`(?m)^\s+at\s+\S+\.\S+\(.*:\d+\)`)
	if stackTracePattern.MatchString(content) {
		score += 30
	}
	// Generic stack frame pattern: ./path/file.go:123 or /path/file.go:123
	stackFramePattern := regexp.MustCompile(`(?m)^\s*(?:at\s+)?[/.\w]+/[\w./-]+\.\w+:\d+`)
	if stackFramePattern.MatchString(content) {
		score += 20
	}

	// File paths (paths starting with /, ./ or containing file extensions)
	filePathPattern := regexp.MustCompile(`(?:/\w+)+/[\w.-]+\.\w+`)
	filePaths := filePathPattern.FindAllString(content, -1)
	score += len(filePaths) * 10

	// Function/method names (identifier followed by parentheses)
	funcPattern := regexp.MustCompile(`\b[a-zA-Z_]\w*\(.*?\)`)
	funcNames := funcPattern.FindAllString(content, -1)
	// Filter out common noise words
	noiseWords := map[string]bool{"if": true, "for": true, "when": true, "with": true, "not": true, "and": true, "or": true, "the": true, "but": true, "has": true, "had": true, "get": true, "got": true, "set": true, "use": true, "used": true, "see": true, "say": true, "says": true, "make": true, "made": true, "take": true, "took": true, "put": true, "run": true, "ran": true, "try": true, "tried": true, "let": true, "log": true, "cat": true, "ls": true}
	signalFuncCount := 0
	for _, f := range funcNames {
		// Keep only if it looks like a real function (longer than 2 chars, contains lowercase after uppercase, etc.)
		name := strings.SplitN(f, "(", 1)[0]
		if !noiseWords[name] && len(name) > 2 {
			signalFuncCount++
		}
	}
	score += signalFuncCount * 10

	// Numerical results/measurements (numbers near common units or indicators)
	numericPattern := regexp.MustCompile(`\b\d+[.,]?\d*\s*(?:passed|failed|errors?|tests?|coverage|ms|s|bytes?|KB|MB|GB|lines?|files?|%|percent|of|total)\b`)
	numerics := numericPattern.FindAllString(content, -1)
	score += len(numerics) * 5

	// General numerical values that look like measurements (percentages, large numbers)
	measurementPattern := regexp.MustCompile(`\b\d+[.,]\d+%?\b`)
	measurements := measurementPattern.FindAllString(content, -1)
	score += len(measurements) * 3

	// Message length adjustments
	if len(content) < 100 {
		score -= 15 // Very short messages are likely low-value
	} else if len(content) > 5000 {
		score -= 10 // Very long verbose messages may be noise
	}
	if len(content) > 200 {
		score += 5 // Substantial content has some value
	}

	return score
}

// compactableMessage represents a message that has been identified as eligible
// for compaction, along with its salience score and original index.
type compactableMessage struct {
	index           int
	score           int
	originalContent string
	originalTokens  int
}

// Compact scans the conversation history and replaces tool-result messages
// with LLM-generated summaries. It always attempts compaction regardless of
// the estimated token count — the caller decides whether to call Compact
// (e.g. auto-compaction gates in run.go).
//
// Compaction stops once the total falls below the LowWater threshold.
// The LowWater stop condition prevents runaway compaction of the entire history.
//
// When SalienceEnabled is true (default), the scan collects all compactable
// messages, scores each by heuristic salience, sorts by score ascending,
// and compacts from lowest score (least important) first. Messages whose
// salience score exceeds HighSalienceSkipThreshold are skipped entirely.
// When SalienceEnabled is false, the original greedy oldest-first behaviour
// is used.
//
// If a summarization call fails for one message, it is skipped and
// compaction continues with the next one.
//
// Tool call argument pruning: if ToolCallRetentionTurns > 0, assistant messages
// beyond the last N (counting only assistant messages) have their
// ToolCall.Function.Arguments replaced with a compact placeholder.
// The ID and Function.Name are preserved. Already-pruned tool calls (detectable
// via the "pruned" JSON prefix) are not re-pruned.
//
// Returns the modified message list (a copy) if any compaction occurred,
// or nil if no compaction was needed. The original slice is never modified.
// compactedCount and freedTokens report the number of messages compacted
// and the approximate token count saved.
// prunedToolCalls reports how many tool call argument blocks were pruned.
func (c *Compactor) Compact(ctx context.Context, messages []message.Message, client *litellm.Client, thresholds Thresholds) (compacted []message.Message, compactedCount int, freedTokens int, prunedToolCalls int, err error) {
	if thresholds.HighWater <= 0 {
		thresholds.HighWater = 90 // sensible default
	}
	if thresholds.LowWater <= 0 {
		thresholds.LowWater = 30
	}
	if thresholds.LowWater >= thresholds.HighWater {
		thresholds.LowWater = thresholds.HighWater / 3
	}

	// Work on a copy so the original is never mutated.
	result := make([]message.Message, len(messages))
	copy(result, messages)

	compactedCount = 0
	freedTokens = 0
	prunedToolCalls = 0

	// --- Pass 1: Tool call argument pruning (always runs, independent of salience) ---
	totalAssistantMsgs := 0
	for _, m := range result {
		if m.Role == "assistant" {
			totalAssistantMsgs++
		}
	}

	assistantIndex := 0 // 0-based index among assistant messages only
	for i := 0; i < len(result); i++ {
		if result[i].Role == "assistant" {
			isWithinRetention := false
			if thresholds.ToolCallRetentionTurns > 0 {
				isWithinRetention = assistantIndex+thresholds.ToolCallRetentionTurns >= totalAssistantMsgs
			}
			if !isWithinRetention && len(result[i].ToolCalls) > 0 {
				for j := range result[i].ToolCalls {
					tc := &result[i].ToolCalls[j]
					args := tc.Function.Arguments
					if args == "" {
						continue
					}
					// Skip already-pruned tool calls (detectable via the placeholder prefix).
					if strings.HasPrefix(args, `{"pruned": "~`) {
						continue
					}
					// Replace arguments with compact placeholder.
					placeholder := fmt.Sprintf(`{"pruned": "~%d chars"}`, len(args))
					prunedTokens := tokenEstimate(args, nil, "")
					tc.Function.Arguments = placeholder
					freedTokens += prunedTokens - tokenEstimate(placeholder, nil, "")
					prunedToolCalls++
				}
			}
			assistantIndex++
		}
	}

	// --- Pass 2: Content summarization ---
	if thresholds.SalienceEnabled {
		// Salience-scored ordering: collect eligible messages, score, sort, compact.
		result, compactedCount, freedTokens = c.compactBySalience(ctx, result, client, thresholds)
	} else {
		// Legacy oldest-first greedy behaviour.
		result, compactedCount, freedTokens = c.compactOldestFirst(ctx, result, client, thresholds)
	}

	if compactedCount == 0 && prunedToolCalls == 0 {
		return nil, 0, 0, 0, nil
	}

	return result, compactedCount, freedTokens, prunedToolCalls, nil
}

// compactBySalience collects all compactable messages, scores them by heuristic
// salience, sorts by score ascending (lowest first), and compacts from the
// least important message upwards. Messages with a salience score above
// thresholds.HighSalienceSkipThreshold are skipped entirely. The low-water stop
// condition is checked after each compaction.
func (c *Compactor) compactBySalience(ctx context.Context, messages []message.Message, client *litellm.Client, thresholds Thresholds) ([]message.Message, int, int) {
	result := make([]message.Message, len(messages))
	copy(result, messages)

	compactedCount := 0
	freedTokens := 0

	// Collect all compactable messages with their scores.
	var candidates []compactableMessage
	for i := 0; i < len(result); i++ {
		// Skip messages that don't have compactable content.
		if result[i].Content == "" {
			continue
		}
		// Skip already-compacted messages.
		if strings.HasPrefix(result[i].Content, "[TOOL RESULT COMPACTED") ||
			strings.HasPrefix(result[i].Content, "[MESSAGE COMPACTED") {
			continue
		}
		// Only compact tool, user, and assistant messages (skip system messages).
		if result[i].Role != "tool" && result[i].Role != "user" && result[i].Role != "assistant" {
			continue
		}
		// Apply per-message size threshold.
		if thresholds.MessageSizeThreshold > 0 {
			est := tokenEstimate(result[i].Content, nil, "")
			for _, tc := range result[i].ToolCalls {
				est += tokenEstimate(tc.Function.Arguments, nil, "")
			}
			if est < thresholds.MessageSizeThreshold {
				continue
			}
		}
		// Score the message content.
		score := salienceScore(result[i].Content)

		// Skip very high salience messages entirely.
		if thresholds.HighSalienceSkipThreshold > 0 && score >= thresholds.HighSalienceSkipThreshold {
			continue
		}

		candidates = append(candidates, compactableMessage{
			index:           i,
			score:           score,
			originalContent: result[i].Content,
			originalTokens:  tokenEstimate(result[i].Content, nil, ""),
		})
	}

	// Sort candidates by score ascending (least important first).
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].score < candidates[b].score
	})

	// Compact candidates in order.
	totalEst := messagesTokenEstimate(result, nil, "")
	for _, cand := range candidates {
		// Check low-water before each compaction.
		if totalEst <= thresholds.LowWater {
			break
		}

		originalContent := result[cand.index].Content
		originalTokens := tokenEstimate(originalContent, nil, "")

		// Call LLM to summarise.
		prompt := summarizationPrompt(result[cand.index].Role, originalContent)
		litellmReq := &litellm.Request{
			Model: "compactor",
			Messages: []litellm.Message{
				{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: prompt}}},
			},
		}
		resp, err := client.Chat(ctx, *litellmReq)
		if err != nil {
			// Skip this one, continue with the next.
			continue
		}

		summary := strings.TrimSpace(resp.Text())
		if summary == "" {
			continue
		}

		// Use different prefix depending on role.
		if result[cand.index].Role == "tool" {
			result[cand.index].Content = fmt.Sprintf("[TOOL RESULT COMPACTED - originally %d tokens] %s", originalTokens, summary)
		} else {
			result[cand.index].Content = fmt.Sprintf("[MESSAGE COMPACTED - originally %d tokens] %s", originalTokens, summary)
		}
		compactedCount++
		freedTokens += originalTokens - tokenEstimate(result[cand.index].Content, nil, "")

		// Re-estimate total.
		totalEst = messagesTokenEstimate(result, nil, "")
	}

	return result, compactedCount, freedTokens
}

// compactOldestFirst implements the original greedy oldest-first scan:
// iterate from oldest to newest and compact the first eligible message found.
func (c *Compactor) compactOldestFirst(ctx context.Context, messages []message.Message, client *litellm.Client, thresholds Thresholds) ([]message.Message, int, int) {
	result := make([]message.Message, len(messages))
	copy(result, messages)

	compactedCount := 0
	freedTokens := 0
	totalEst := messagesTokenEstimate(result, nil, "")

	for i := 0; i < len(result); i++ {
		// --- Content summarization ---
		if result[i].Content == "" {
			continue
		}
		// Skip already-compacted messages.
		if strings.HasPrefix(result[i].Content, "[TOOL RESULT COMPACTED") ||
			strings.HasPrefix(result[i].Content, "[MESSAGE COMPACTED") {
			continue
		}
		// Apply per-message size threshold.
		if thresholds.MessageSizeThreshold > 0 {
			est := tokenEstimate(result[i].Content, nil, "")
			for _, tc := range result[i].ToolCalls {
				est += tokenEstimate(tc.Function.Arguments, nil, "")
			}
			if est < thresholds.MessageSizeThreshold {
				continue
			}
		}
		// Only compact tool, user, and assistant messages.
		if result[i].Role != "tool" && result[i].Role != "user" && result[i].Role != "assistant" {
			continue
		}

		originalContent := result[i].Content
		originalTokens := tokenEstimate(originalContent, nil, "")

		prompt := summarizationPrompt(result[i].Role, originalContent)
		litellmReq := &litellm.Request{
			Model: "compactor",
			Messages: []litellm.Message{
				{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: prompt}}},
			},
		}
		resp, err := client.Chat(ctx, *litellmReq)
		if err != nil {
			continue
		}

		summary := strings.TrimSpace(resp.Text())
		if summary == "" {
			continue
		}

		if result[i].Role == "tool" {
			result[i].Content = fmt.Sprintf("[TOOL RESULT COMPACTED - originally %d tokens] %s", originalTokens, summary)
		} else {
			result[i].Content = fmt.Sprintf("[MESSAGE COMPACTED - originally %d tokens] %s", originalTokens, summary)
		}
		compactedCount++
		freedTokens += originalTokens - tokenEstimate(result[i].Content, nil, "")

		totalEst = messagesTokenEstimate(result, nil, "")
		if totalEst <= thresholds.LowWater {
			break
		}
	}

	return result, compactedCount, freedTokens
}
