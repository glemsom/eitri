package engine

import (
	_ "embed"
	"strings"
)

//go:embed prompt.md
var SystemPrompt string

//go:embed prompt_yolo.md
var SystemPromptYolo string

// MaxSystemPromptTokens is the hard ceiling on the embedded system prompt.
const MaxSystemPromptTokens = 1200

// SystemPromptContent trims the trailing newline from the embedded prompt.
func SystemPromptContent() string {
	return strings.TrimRight(SystemPrompt, "\n")
}

// SystemPromptYoloContent returns the unsandboxed (--yolo-unsafe) variant of
// the embedded persona, byte-identical to the default: the batch-subagent
// recipe moved to the `subagents` builtin skill, leaving both heads with only
// the one-line pointer.
func SystemPromptYoloContent() string {
	return strings.TrimRight(SystemPromptYolo, "\n")
}
