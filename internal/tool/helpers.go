package tool

import (
	"context"
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

// SubAgentManager is the seam interface for spawning and collecting sub-agents.
// The runner package implements this; tools only depend on the interface.
type SubAgentManager interface {
	// SpawnSubAgent starts a sub-agent in the background and returns a task ID.
	// The sessionID comes from context via SessionIDKey.
	SpawnSubAgent(ctx context.Context, sessionID, task string, maxTurns int) (taskID string, err error)

	// CollectSubAgents blocks until all listed tasks complete or ctx cancels.
	CollectSubAgents(ctx context.Context, taskIDs []string) (map[string]SubAgentResult, error)
}

// SubAgentResult holds the outcome of one sub-agent task.
type SubAgentResult struct {
	Status    string `json:"status"`
	Result    string `json:"result"`
	TurnCount int    `json:"turn_count"`
}
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
