package runner

import (
	"context"
	"fmt"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/litellm"
	uisession "github.com/glemsom/eitri/internal/session"
)

// ── sessionHistoryManager ──────────────────────────────────────────────────

// sessionHistoryManager implements HistoryManager for the browser UI path.
// It wraps *history.SessionManager, *uisession.Manager, and the sessionID
// that was supplied when RunAgent was called.
type sessionHistoryManager struct {
	sessionMgr   *history.SessionManager
	uisessionMgr *uisession.Manager
	sessionID    string
}

// newSessionHistoryManager creates a sessionHistoryManager.
// The sessionID is baked in because it is known at construction time.
func newSessionHistoryManager(sessionMgr *history.SessionManager, uisessionMgr *uisession.Manager, sessionID string) *sessionHistoryManager {
	return &sessionHistoryManager{
		sessionMgr:   sessionMgr,
		uisessionMgr: uisessionMgr,
		sessionID:    sessionID,
	}
}

// History returns the conversation history from the session manager.
// The ignored parameter is only present to satisfy the interface signature.
func (m *sessionHistoryManager) History(_ string) []litellm.Message {
	if m.sessionMgr == nil {
		return nil
	}
	return m.sessionMgr.History(m.sessionID)
}

// AppendAssistant appends an assistant message to the session manager.
func (m *sessionHistoryManager) AppendAssistant(_ string, content string, toolCalls []litellm.ToolCall) {
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
// messages path. It wraps *litellm.Request and appends messages directly
// to req.Messages. The caller must ensure the request already has its
// initial messages set (system + user). History() simply returns the current
// req.Messages.
type requestHistoryManager struct {
	req *litellm.Request
}

// newRequestHistoryManager creates a requestHistoryManager.
func newRequestHistoryManager(req *litellm.Request) *requestHistoryManager {
	return &requestHistoryManager{req: req}
}

// History returns req.Messages as-is.
func (m *requestHistoryManager) History(_ string) []litellm.Message {
	return m.req.Messages
}

// AppendAssistant appends an assistant message to req.Messages.
func (m *requestHistoryManager) AppendAssistant(_ string, content string, toolCalls []litellm.ToolCall) {
	m.req.Messages = append(m.req.Messages, litellm.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	})
}

// AppendTool appends a tool result message to req.Messages.
func (m *requestHistoryManager) AppendTool(_ string, toolCallID, content string, isError bool) {
	_ = isError // The error flag is not stored in litellm.Message; content conveys it.
	m.req.Messages = append(m.req.Messages, litellm.Message{
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

// newTestConfirmerStub creates a testConfirmerStub that always returns
// the given result and error.
func newTestConfirmerStub(result *ConfirmationResult, err error) *testConfirmerStub {
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
