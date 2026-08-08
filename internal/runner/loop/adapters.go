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
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persona"
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
// *session.Manager, the canonical store — issue #1241) and
// requestHistoryManager (headless/direct-messages path via *litellm.Request).
// Both support ReplaceHistory so auto-compaction can write the compacted
// history back regardless of the storage backend.
type HistoryManager interface {
	// History returns the full conversation history with system prompt prepended.
	History() []message.EitriMessage

	// AppendAssistant appends an assistant message with text content and
	// optional tool calls (as litellm.ToolUseBlock).
	AppendAssistant(content string, toolCalls []litellm.ToolUseBlock)

	// AppendTool appends a tool result message.
	// rawContent is the pre-compression output (empty when compression did not apply).
	AppendTool(toolCallID, content, rawContent string, isError bool)

	// ReplaceHistory replaces the full conversation history with the given
	// flat messages. Used by auto-compaction to write the compacted history
	// back — both session-manager-backed and request-based histories support
	// it (issue #1096).
	ReplaceHistory(messages []message.Message)

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

// sessionHistoryManager implements HistoryManager for the browser UI path. It
// wraps *session.Manager — the canonical conversation store (issue #1241) —
// and the sessionID that was supplied when RunAgent was called. The loop
// reads and writes the canonical store directly, so the per-turn
// history→conversation copy in the turn-completer disappears for
// session-backed runs: the UI conversation, the LLM request history, and the
// persisted snapshots all read the same store.
type sessionHistoryManager struct {
	sessionMgr *uisession.Manager
	sessionID  string
}

// NewSessionHistoryManager creates a sessionHistoryManager.
// The sessionID is baked in because it is known at construction time.
func NewSessionHistoryManager(sessionMgr *uisession.Manager, sessionID string) *sessionHistoryManager {
	return &sessionHistoryManager{
		sessionMgr: sessionMgr,
		sessionID:  sessionID,
	}
}

// History returns the conversation history from the canonical store with the
// system prompt prepended. Mirrors history.SessionManager.History: the system
// prompt is stored separately on the session (never in the message list, the
// strip-system-message invariant, ADR-0028) and prepended as the leading
// system message on reads.
//
// The conversation is read through Manager.CopyConversation — a snapshot
// taken under the manager lock — never through the live shared reference
// (GetConversationShared): manual compaction (CompactSession → History) runs
// on the API goroutine with no active-run guard, so it can overlap the run
// goroutine's AppendAssistant/AppendTool appends to the same conversation
// (issue #1241 fix round). Iterating the live reference without the lock is a
// data race (torn reads can drop/duplicate messages in the compactor's
// output); the locked copy is immune.
//
// Fidelity note: each canonical message is round-tripped through
// message.FromLitellm(message.ToLitellmMessage(msg)) so LLM request history
// stays byte-identical to the old store (the parity test). The round-trip
// folds ReasoningContent into the content TextBlock ("R\nC") for non-tool
// assistant messages and drops RawContent — a deliberate fidelity match to
// the old store, whose LLM history never carried either field. Adapter-written
// messages carry no reasoning, so the fold only affects messages written by
// other paths (syncRunResultToUISession, persister-less configs).
//
// UI-only empty assistant placeholders (created by session.Manager's
// AppendComponent/SetQuickReplies when the last conversation message is not an
// assistant message — the second and subsequent component-emitting tool calls
// of a turn) are filtered out on read: the old LLM-history store never carried
// them, and they serialise as {"role":"assistant"} with no content or
// tool_calls, which some providers reject. The filter is read-side — the
// canonical store keeps the placeholders as the UI component targets; because
// compaction (auto and manual) reads and rewrites the conversation through
// this LLM-view History(), a compaction also drops the empty placeholder
// scaffolding (components attached to real, non-empty assistant messages
// survive).
func (m *sessionHistoryManager) History() []message.EitriMessage {
	if m.sessionMgr == nil {
		return nil
	}
	convo := m.sessionMgr.CopyConversation(m.sessionID)
	if convo == nil {
		return nil
	}

	// Build the system prompt message. The canonical store stores the prompt
	// separately; fall back to the canonical persona default when unset,
	// exactly like the history store.
	sysPrompt := convo.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = persona.DefaultPrompt
	}
	sysMsg := message.EitriMessage{
		Message: litellm.Message{
			Role:   litellm.Role("system"),
			Blocks: []litellm.Block{litellm.TextBlock{Text: sysPrompt}},
		},
		CreatedAt: time.Now(),
	}

	messages := make([]message.EitriMessage, 0, 1+len(convo.Messages))
	messages = append(messages, sysMsg)
	for _, msg := range convo.Messages {
		em := message.FromLitellm(message.ToLitellmMessage(msg))
		if isEmptyAssistant(em) {
			continue
		}
		em.CreatedAt = msg.CreatedAt
		em.Components = msg.Components
		em.QuickReplies = msg.QuickReplies
		messages = append(messages, em)
	}
	return messages
}

// isEmptyAssistant reports whether an EitriMessage is a UI-only empty
// assistant placeholder: assistant role with no text content and no tool
// calls. These are appended into the canonical store by session.Manager's
// AppendComponent/SetQuickReplies when the last conversation message is not an
// assistant message (components need a target to attach to). The old LLM
// history store never carried them, so the session-backed History() filters
// them on read (see History's fidelity note). Assistant messages with content
// or tool calls are never empty and must pass through.
func isEmptyAssistant(m message.EitriMessage) bool {
	if m.Role != litellm.Role("assistant") {
		return false
	}
	return m.Content() == "" && len(m.ToolCalls()) == 0
}

// AppendAssistant appends an assistant message to the canonical conversation.
// Empty assistant messages (no content, no tool calls) are skipped — they
// serialise as {"role":"assistant"} with no content or tool_calls, which some
// providers reject (matches the history store's append path so LLM request
// history stays byte-identical).
func (m *sessionHistoryManager) AppendAssistant(content string, toolCalls []litellm.ToolUseBlock) {
	if m.sessionMgr == nil {
		return
	}
	if content == "" && len(toolCalls) == 0 {
		return
	}
	m.sessionMgr.AppendToConversation(m.sessionID, message.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toMessageToolCalls(toolCalls),
		CreatedAt: time.Now(),
	})
}

// AppendTool appends a tool result message to the canonical conversation.
// rawContent is the pre-compression output (empty when compression did not apply).
func (m *sessionHistoryManager) AppendTool(toolCallID, content, rawContent string, isError bool) {
	if m.sessionMgr == nil {
		return
	}
	msg := message.Message{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    content,
		RawContent: rawContent,
		CreatedAt:  time.Now(),
	}
	m.sessionMgr.AppendToConversation(m.sessionID, msg)
}

// ReplaceHistory replaces the canonical conversation with the given flat
// messages (e.g. compacted history written back after auto-compaction). A
// leading system message is extracted into the session's separate SystemPrompt
// field, mirroring history.SessionManager.RestoreHistory (the
// strip-system-message invariant, ADR-0028).
func (m *sessionHistoryManager) ReplaceHistory(messages []message.Message) {
	if m.sessionMgr == nil {
		return
	}
	if len(messages) > 0 && messages[0].Role == "system" {
		m.sessionMgr.SetSystemPrompt(m.sessionID, messages[0].Content)
		m.sessionMgr.ReplaceConversationMessages(m.sessionID, messages[1:])
		return
	}
	m.sessionMgr.ReplaceConversationMessages(m.sessionID, messages)
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
func (m *requestHistoryManager) AppendTool(toolCallID, content, rawContent string, isError bool) {
	_ = isError    // The error flag is not stored; content conveys it.
	_ = rawContent // Not stored on the request (only used for snapshots).
	m.req.Messages = append(m.req.Messages, toolResultToLitellm(toolCallID, content))
}

// ReplaceHistory replaces req.Messages with the given flat messages, converting
// them back to litellm transport format. This is how auto-compaction writes
// the compacted history back for request-based (sub-agent) histories.
func (m *requestHistoryManager) ReplaceHistory(messages []message.Message) {
	lm := make([]litellm.Message, 0, len(messages))
	for _, msg := range messages {
		lm = append(lm, message.ToLitellmMessage(msg))
	}
	m.req.Messages = lm
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
