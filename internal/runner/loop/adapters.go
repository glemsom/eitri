// Package loop provides HistoryManager and Confirmer interfaces and their
// implementations — the seam between the agent loop and its runtime context
// (browser UI sessions vs headless/direct-messages mode).
//
// Merged from the former runner/adapters sub-package (issues #860, #691).
package loop

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
)

// ── Value types ─────────────────────────────────────────────────────────────

// ConfirmationResult carries the user's decision for a confirmation prompt.
type ConfirmationResult struct {
	Path     string
	Approved bool
}

// ConfirmationFunc is called when a tool needs user confirmation before
// proceeding. It sends the confirmation request and blocks until the user
// responds or the context is cancelled.
type ConfirmationFunc func(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)

// ── HistoryManager ──────────────────────────────────────────────────────────

// HistoryManager abstracts conversation history storage for the agent loop.
// Two adapters exist: sessionHistoryManager (browser UI path via
// *history.SessionManager) and requestHistoryManager (headless/direct-messages
// path via *litellm.Request).
type HistoryManager interface {
	// History returns the full conversation history with system prompt prepended.
	History() []message.EitriMessage

	// AppendAssistant appends an assistant message with text content and
	// optional tool calls (as litellm.ToolUseBlock).
	AppendAssistant(content string, toolCalls []litellm.ToolUseBlock)

	// AppendTool appends a tool result message.
	AppendTool(toolCallID, content string, isError bool)

	// RequestBased returns true when history is stored directly on the
	// *litellm.Request (requestHistoryManager) rather than in a session manager.
	// When true, the caller must trim req.Messages directly when caps are set.
	RequestBased() bool
}

// ── Confirmer ───────────────────────────────────────────────────────────────

// Confirmer abstracts the user-confirmation flow for the agent loop.
// The production implementation uses a channel-based mechanism via
// RunService.confirmPath; testConfirmerStub provides a canned result.
type Confirmer interface {
	// Confirm blocks until the user approves or denies the path, or the
	// context is cancelled. Returns the confirmation result or an error.
	Confirm(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)
}

// ── sessionHistoryManager ──────────────────────────────────────────────────

// sessionHistoryManager implements HistoryManager for the browser UI path.
// It wraps *history.SessionManager and the sessionID that was supplied when
// RunAgent was called.
type sessionHistoryManager struct {
	sessionMgr *history.SessionManager
	sessionID  string
}

// NewSessionHistoryManager creates a sessionHistoryManager.
// The sessionID is baked in because it is known at construction time.
func NewSessionHistoryManager(sessionMgr *history.SessionManager, sessionID string) *sessionHistoryManager {
	return &sessionHistoryManager{
		sessionMgr: sessionMgr,
		sessionID:  sessionID,
	}
}

// History returns the conversation history from the session manager.
func (m *sessionHistoryManager) History() []message.EitriMessage {
	if m.sessionMgr == nil {
		return nil
	}
	return m.sessionMgr.History(m.sessionID)
}

// AppendAssistant appends an assistant message to the session manager.
func (m *sessionHistoryManager) AppendAssistant(content string, toolCalls []litellm.ToolUseBlock) {
	if m.sessionMgr == nil {
		return
	}
	m.sessionMgr.AppendAssistant(m.sessionID, content, toMessageToolCalls(toolCalls))
}

// AppendTool appends a tool result message to the session manager.
func (m *sessionHistoryManager) AppendTool(toolCallID, content string, isError bool) {
	if m.sessionMgr == nil {
		return
	}
	m.sessionMgr.AppendTool(m.sessionID, toolCallID, content, isError)
}

// RequestBased returns false since this implementation uses a session manager.
func (m *sessionHistoryManager) RequestBased() bool {
	return false
}

// ── requestHistoryManager ──────────────────────────────────────────────────

// requestHistoryManager implements HistoryManager for the headless/direct-
// messages path. It wraps *litellm.Request and appends messages directly
// to req.Messages. The caller must ensure the request already has its
// initial messages set (system + user). History() returns stored messages
// as EitriMessage values via FromLitellm.
type requestHistoryManager struct {
	req *litellm.Request
}

// NewRequestHistoryManager creates a requestHistoryManager.
func NewRequestHistoryManager(req *litellm.Request) *requestHistoryManager {
	return &requestHistoryManager{req: req}
}

// History returns req.Messages as []message.EitriMessage.
func (m *requestHistoryManager) History() []message.EitriMessage {
	out := make([]message.EitriMessage, 0, len(m.req.Messages))
	for _, msg := range m.req.Messages {
		out = append(out, message.FromLitellm(msg))
	}
	return out
}

// AppendAssistant appends an assistant message to req.Messages.
func (m *requestHistoryManager) AppendAssistant(content string, toolCalls []litellm.ToolUseBlock) {
	m.req.Messages = append(m.req.Messages, assistantToLitellmMsg(content, toolCalls))
}

// AppendTool appends a tool result message to req.Messages.
func (m *requestHistoryManager) AppendTool(toolCallID, content string, isError bool) {
	_ = isError // The error flag is not stored; content conveys it.
	m.req.Messages = append(m.req.Messages, toolResultToLitellm(toolCallID, content))
}

// RequestBased returns true since this implementation wraps *litellm.Request.
func (m *requestHistoryManager) RequestBased() bool {
	return true
}

// ── Conversion helpers ────────────────────────────────────────────────────

// assistantToLitellmMsg converts assistant message fields to a litellm.Message.
// Always includes at least a TextBlock so the serialised JSON always has a
// "content" field. Without it, an empty-assistant message produces
// {"role":"assistant"} which is invalid for providers expecting
// OpenAI-compatible format.
func assistantToLitellmMsg(content string, toolCalls []litellm.ToolUseBlock) litellm.Message {
	blocks := make([]litellm.Block, 0, 1+len(toolCalls))
	// Always produce a TextBlock. When content is empty and no tool calls
	// exist, still emit an empty TextBlock so JSON has content:"" instead
	// of omitting "content" entirely.
	if content != "" || len(toolCalls) == 0 {
		blocks = append(blocks, litellm.TextBlock{Text: content})
	}
	for _, tc := range toolCalls {
		args := tc.Arguments
		if !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		blocks = append(blocks, litellm.ToolUseBlock{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: args,
		})
	}
	return litellm.Message{
		Role:   litellm.Role("assistant"),
		Blocks: blocks,
	}
}

// toolResultToLitellm converts tool result fields to a litellm.Message.
func toolResultToLitellm(toolCallID, content string) litellm.Message {
	return litellm.Message{
		Role: litellm.Role("tool"),
		Blocks: []litellm.Block{
			litellm.ToolResultBlock{
				ToolUseID: toolCallID,
				Content:   []litellm.Block{litellm.TextBlock{Text: content}},
			},
		},
	}
}

// toMessageToolCalls converts []litellm.ToolUseBlock to []message.ToolCall.
func toMessageToolCalls(tcs []litellm.ToolUseBlock) []message.ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]message.ToolCall, len(tcs))
	for i, tc := range tcs {
		args := ""
		if len(tc.Arguments) > 0 {
			args = string(tc.Arguments)
		}
		out[i] = message.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: message.FunctionCall{
				Name:      tc.Name,
				Arguments: args,
			},
		}
	}
	return out
}

// ── testConfirmerStub ─────────────────────────────────────────────────────

// testConfirmerStub implements Confirmer for unit tests. It returns a canned
// result for every Confirm call.
type testConfirmerStub struct {
	result *ConfirmationResult
	err    error
}

// NewTestConfirmerStub creates a testConfirmerStub that always returns
// the given result and error.
func NewTestConfirmerStub(result *ConfirmationResult, err error) *testConfirmerStub {
	return &testConfirmerStub{result: result, err: err}
}

// Confirm returns the canned result and error.
func (s *testConfirmerStub) Confirm(_ context.Context, sessionID, path, message string) (*ConfirmationResult, error) {
	_ = sessionID
	_ = path
	_ = message
	if s.err != nil {
		return nil, fmt.Errorf("testConfirmerStub: %w", s.err)
	}
	return s.result, nil
}

// ── funcConfirmer ────────────────────────────────────────────────────────

// funcConfirmer implements Confirmer by wrapping a ConfirmationFunc.
// This adapter allows existing callers that pass a function to continue
// working after RunAgent switches to the Confirmer interface.
type funcConfirmer struct {
	fn ConfirmationFunc
}

// NewFuncConfirmer creates a funcConfirmer from a ConfirmationFunc.
func NewFuncConfirmer(fn ConfirmationFunc) Confirmer {
	return &funcConfirmer{fn: fn}
}

// Confirm delegates to the wrapped ConfirmationFunc.
func (c *funcConfirmer) Confirm(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error) {
	return c.fn(ctx, sessionID, path, message)
}
