// Package adapters provides HistoryManager and Confirmer interfaces and their
// implementations — the seam between the agent loop and its runtime context
// (browser UI sessions vs headless/direct-messages mode).
//
// Extracted from the runner monolith (ticket #691).
package adapters

import (
	"context"
	"fmt"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/llm"
	uisession "github.com/glemsom/eitri/internal/session"
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
// path via *llm.Request).
type HistoryManager interface {
	// History returns the full conversation history with system prompt prepended.
	History(sessionID string) []llm.Message

	// AppendAssistant appends an assistant message with text content and
	// optional tool calls.
	AppendAssistant(sessionID, content string, toolCalls []llm.ToolCall)

	// AppendTool appends a tool result message.
	AppendTool(sessionID, toolCallID, content string, isError bool)
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
// It wraps *history.SessionManager, *uisession.Manager, and the sessionID
// that was supplied when RunAgent was called.
type sessionHistoryManager struct {
	sessionMgr   *history.SessionManager
	uisessionMgr *uisession.Manager
	sessionID    string
}

// NewSessionHistoryManager creates a sessionHistoryManager.
// The sessionID is baked in because it is known at construction time.
func NewSessionHistoryManager(sessionMgr *history.SessionManager, uisessionMgr *uisession.Manager, sessionID string) *sessionHistoryManager {
	return &sessionHistoryManager{
		sessionMgr:   sessionMgr,
		uisessionMgr: uisessionMgr,
		sessionID:    sessionID,
	}
}

// History returns the conversation history from the session manager.
// The ignored parameter is only present to satisfy the interface signature.
func (m *sessionHistoryManager) History(_ string) []llm.Message {
	if m.sessionMgr == nil {
		return nil
	}
	return m.sessionMgr.History(m.sessionID)
}

// AppendAssistant appends an assistant message to the session manager.
func (m *sessionHistoryManager) AppendAssistant(_ string, content string, toolCalls []llm.ToolCall) {
	if m.sessionMgr == nil {
		return
	}
	m.sessionMgr.AppendAssistant(m.sessionID, content, toolCalls)
}

// AppendTool appends a tool result message to the session manager.
func (m *sessionHistoryManager) AppendTool(_ string, toolCallID, content string, isError bool) {
	if m.sessionMgr == nil {
		return
	}
	m.sessionMgr.AppendTool(m.sessionID, toolCallID, content, isError)
}

// ── requestHistoryManager ──────────────────────────────────────────────────

// requestHistoryManager implements HistoryManager for the headless/direct-
// messages path. It wraps *llm.Request and appends messages directly
// to req.Messages. The caller must ensure the request already has its
// initial messages set (system + user). History() simply returns the current
// req.Messages.
type requestHistoryManager struct {
	req *llm.Request
}

// NewRequestHistoryManager creates a requestHistoryManager.
func NewRequestHistoryManager(req *llm.Request) *requestHistoryManager {
	return &requestHistoryManager{req: req}
}

// IsRequestBasedHistory returns true when the HistoryManager is the
// request-based variant (wrapping *llm.Request), meaning history is
// stored directly on the request and must be trimmed by the caller
// when caps are set.
func IsRequestBasedHistory(mgr HistoryManager) bool {
	_, ok := mgr.(*requestHistoryManager)
	return ok
}

// History returns req.Messages as-is.
func (m *requestHistoryManager) History(_ string) []llm.Message {
	return m.req.Messages
}

// AppendAssistant appends an assistant message to req.Messages.
func (m *requestHistoryManager) AppendAssistant(_ string, content string, toolCalls []llm.ToolCall) {
	m.req.Messages = append(m.req.Messages, llm.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	})
}

// AppendTool appends a tool result message to req.Messages.
func (m *requestHistoryManager) AppendTool(_ string, toolCallID, content string, isError bool) {
	_ = isError // The error flag is not stored in llm.Message; content conveys it.
	m.req.Messages = append(m.req.Messages, llm.Message{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    content,
	})
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
