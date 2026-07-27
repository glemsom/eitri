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
	"github.com/glemsom/eitri/internal/llm"
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
	History() []llm.Message

	// AppendAssistant appends an assistant message with text content and
	// optional tool calls.
	AppendAssistant(content string, toolCalls []llm.ToolCall)

	// AppendTool appends a tool result message.
	AppendTool(toolCallID, content string, isError bool)

	// RequestBased returns true when history is stored directly on the
	// *llm.Request (requestHistoryManager) rather than in a session manager.
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
func (m *sessionHistoryManager) History() []llm.Message {
	if m.sessionMgr == nil {
		return nil
	}
	return m.sessionMgr.History(m.sessionID)
}

// AppendAssistant appends an assistant message to the session manager.
func (m *sessionHistoryManager) AppendAssistant(content string, toolCalls []llm.ToolCall) {
	if m.sessionMgr == nil {
		return
	}
	m.sessionMgr.AppendAssistant(m.sessionID, content, toolCalls)
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
// initial messages set (system + user). History() converts the stored
// litellm.Message values back to llm.Message for interface compliance.
type requestHistoryManager struct {
	req *litellm.Request
}

// NewRequestHistoryManager creates a requestHistoryManager.
func NewRequestHistoryManager(req *litellm.Request) *requestHistoryManager {
	return &requestHistoryManager{req: req}
}

// History returns req.Messages converted to []llm.Message.
func (m *requestHistoryManager) History() []llm.Message {
	out := make([]llm.Message, 0, len(m.req.Messages))
	for _, msg := range m.req.Messages {
		out = append(out, litellmMessageToLLM(msg))
	}
	return out
}

// AppendAssistant appends an assistant message to req.Messages.
func (m *requestHistoryManager) AppendAssistant(content string, toolCalls []llm.ToolCall) {
	m.req.Messages = append(m.req.Messages, assistantToLitellm(content, toolCalls))
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

// ── Conversion helpers (litellm ↔ llm) ────────────────────────────────────

// litellmMessageToLLM converts a litellm.Message back to llm.Message.
// It extracts text content from TextBlock, tool calls from ToolUseBlock,
// and tool results from ToolResultBlock.
func litellmMessageToLLM(msg litellm.Message) llm.Message {
	out := llm.Message{
		Role: string(msg.Role),
	}
	for _, block := range msg.Blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			out.Content += b.Text
		case litellm.ToolUseBlock:
			if out.ToolCalls == nil {
				out.ToolCalls = make([]llm.ToolCall, 0)
			}
			args := ""
			if len(b.Arguments) > 0 {
				args = string(b.Arguments)
			}
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case litellm.ToolResultBlock:
			out.ToolCallID = b.ToolUseID
			for _, content := range b.Content {
				if text, ok := content.(litellm.TextBlock); ok {
					out.Content += text.Text
				}
			}
		}
	}
	return out
}

// assistantToLitellm converts an assistant message fields to a litellm.Message.
func assistantToLitellm(content string, toolCalls []llm.ToolCall) litellm.Message {
	var blocks []litellm.Block
	if content != "" {
		blocks = append(blocks, litellm.TextBlock{Text: content})
	}
	for _, tc := range toolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		blocks = append(blocks, litellm.ToolUseBlock{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	if blocks == nil {
		blocks = []litellm.Block{litellm.TextBlock{Text: content}}
	}
	return litellm.Message{
		Role:   litellm.Role("assistant"),
		Blocks: blocks,
	}
}

// toolResultToLitellm converts a tool result message fields to a litellm.Message.
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
