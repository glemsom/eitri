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
// the embedded persona: byte-identical to the default prompt except the subagent
// guidance, which never claims a terminating sandbox because no cage runs.
func SystemPromptYoloContent() string {
	return strings.TrimRight(SystemPromptYolo, "\n")
}
