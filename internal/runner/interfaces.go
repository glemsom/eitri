package runner

import (
	"context"

	"github.com/glemsom/eitri/internal/litellm"
)

// HistoryManager abstracts conversation history storage for the agent loop.
// Two adapters exist: sessionHistoryManager (browser UI path via
// *history.SessionManager) and requestHistoryManager (headless/direct-messages
// path via *litellm.Request).
type HistoryManager interface {
	// History returns the full conversation history with system prompt prepended.
	History(sessionID string) []litellm.Message

	// AppendAssistant appends an assistant message with text content and
	// optional tool calls.
	AppendAssistant(sessionID, content string, toolCalls []litellm.ToolCall)

	// AppendTool appends a tool result message.
	AppendTool(sessionID, toolCallID, content string, isError bool)
}

// Confirmer abstracts the user-confirmation flow for the agent loop.
// The production implementation uses a channel-based mechanism via
// RunService.confirmPath; testConfirmerStub provides a canned result.
type Confirmer interface {
	// Confirm blocks until the user approves or denies the path, or the
	// context is cancelled. Returns the confirmation result or an error.
	Confirm(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)
}
