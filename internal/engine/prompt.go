package engine

import (
	_ "embed"
	"strings"
)

//go:embed prompt.md
var SystemPrompt string

// MaxSystemPromptTokens is the hard ceiling on the embedded system prompt.
const MaxSystemPromptTokens = 1000

// CountTokens estimates the token count of s for the ceiling gate.
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
