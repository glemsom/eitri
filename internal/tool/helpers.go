package tool

import (
	"fmt"

	"github.com/voocel/litellm"
)

// ErrNeedsConfirmation is returned by Call when a tool execution requires
// user approval before proceeding. The Path identifies the blocked resource.
// The agent loop should pause, send a needs_confirmation SSE event, and
// wait for user response before retrying or returning an error.
type ErrNeedsConfirmation struct {
	Path    string
	Message string
}

func (e *ErrNeedsConfirmation) Error() string {
	return fmt.Sprintf("needs confirmation: path %q: %s", e.Path, e.Message)
}

// contextKey is a private type for context value keys to avoid collisions.
type contextKey string

// SessionIDKey is the context key used to pass the session ID to tool handlers.
const SessionIDKey contextKey = "tool_session_id"

// sessionIDKey is an alias for backward compatibility.
const sessionIDKey = SessionIDKey

// textBlocks creates a []litellm.Block containing a single TextBlock.
// These are plain content blocks; dispatch wraps them in ToolResultBlock.
func textBlocks(text string) []litellm.Block {
	if text == "" {
		return nil
	}
	return []litellm.Block{litellm.TextBlock{Text: text}}
}
