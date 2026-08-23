package engine

import (
	_ "embed"
	"strings"
)

//go:embed prompt.md
var SystemPrompt string

// MaxSystemPromptTokens is the hard ceiling on the embedded system prompt.
const MaxSystemPromptTokens = 1000

// SystemPromptContent trims the trailing newline from the embedded prompt.
func SystemPromptContent() string {
	return strings.TrimRight(SystemPrompt, "\n")
}
