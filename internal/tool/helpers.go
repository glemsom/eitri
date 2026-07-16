package tool

import (
	"github.com/voocel/litellm"
)

// contextKey is a private type for context value keys to avoid collisions.
type contextKey string

// sessionIDKey is the context key used to pass the session ID to tool handlers.
const sessionIDKey contextKey = "tool_session_id"

// textBlocks creates a []litellm.Block containing a single TextBlock.
// These are plain content blocks; dispatch wraps them in ToolResultBlock.
func textBlocks(text string) []litellm.Block {
	if text == "" {
		return nil
	}
	return []litellm.Block{litellm.TextBlock{Text: text}}
}
