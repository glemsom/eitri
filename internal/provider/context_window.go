package provider

import "strings"

// knownContextWindows maps model name prefixes to their context window token
// limits.  ContextWindowForModel performs a longest-matching-prefix lookup
// against this table.
//
// When adding entries, prefer shorter prefixes for model families so that
// future model variants are matched automatically.  Add a specific entry only
// when a variant has a materially different limit than the family default.
//
// Values are informed by provider documentation as of mid-2025.  They are a
// reasonable default; the user's configured context_window_tokens always wins.
var knownContextWindows = map[string]int{
	// OpenAI GPT-4o family
	"gpt-4o":         128_000,
	"gpt-4o-mini":    128_000,
	"gpt-4o-turbo":   128_000,

	// OpenAI GPT-5 family (first-party references only; subject to change)
	"gpt-5": 200_000,

	// OpenAI o1 / o3 reasoning family
	"o1":         200_000,
	"o1-mini":    128_000,
	"o1-preview": 128_000,
	"o3-mini":    200_000,

	// Anthropic Claude family
	"claude-4":      200_000,
	"claude-3.5":    200_000,
	"claude-3":      200_000,
	"claude-sonnet": 200_000,
	"claude-haiku":  200_000,

	// DeepSeek family
	"deepseek-chat":     128_000,
	"deepseek-reasoner": 128_000,
	"deepseek":          128_000, // catch-all for unknown deepseek-* variants

	// Google Gemini 2.x / 3.x family
	"gemini-2.5": 1_048_576,
	"gemini-2.0": 1_048_576,
	"gemini-3":   1_048_576, // placeholder for future Gemini 3 models

	// Qwen family (Alibaba Cloud)
	"qwen-max":   131_072,
	"qwen-plus":  131_072,
	"qwen-turbo": 131_072,
	"qwen":       131_072, // catch-all for unknown qwen-* variants

	// MiniMax family
	"minimax": 131_072,
}

// ContextWindowForModel returns the context window token limit for the given
// model name by looking up the longest matching prefix in knownContextWindows.
// Returns 0 when no prefix matches.
func ContextWindowForModel(model string) int {
	best := 0
	bestLen := 0
	for prefix, size := range knownContextWindows {
		if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			best = size
			bestLen = len(prefix)
		}
	}
	return best
}
