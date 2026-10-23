package engine

import (
	_ "embed"
	"strings"
)

//go:embed prompt.md
var SystemPrompt string

// MaxSystemPromptTokens is the hard ceiling on the embedded system prompt.
const MaxSystemPromptTokens = 1200

func SystemPromptContent() string {
	return strings.TrimRight(SystemPrompt, "\n")
}
