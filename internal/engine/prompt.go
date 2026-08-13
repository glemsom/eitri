package engine

import (
	_ "embed"
	"strings"
)

//go:embed prompt.md
var SystemPrompt string

// MaxSystemPromptTokens is the hard ceiling on the embedded system prompt.
// Every prompt token is billed every turn as the head of the byte-stable
// cache prefix (spec §34), so the budget is a deliberate, ADR-0005-gated
// invariant rather than a taste call. The gate lives in prompt_test.go.
const MaxSystemPromptTokens = 1000

// CountTokens estimates the token count of s for the ceiling gate. It is a
// deterministic approximation (chars/4, 50-token floor) used only to enforce
// the ADR-0005 budget — not a provider-exact count.
func CountTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 50 {
		return 50
	}
	return n
}

// SystemPromptContent trims the trailing newline from the embedded prompt.
func SystemPromptContent() string {
	return strings.TrimRight(SystemPrompt, "\n")
}
