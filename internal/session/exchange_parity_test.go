package session_test

import (
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/session"
)

// parity test pair — drives the canonical session store and the old
// LLM-history store through the same operation sequence and asserts the
// resulting conversation shapes are identical (issue #1239, acceptance
// criterion: same inputs → same history and trim behaviour in both stores).
//
// The history store's read side (History) returns []EitriMessage with the
// system prompt prepended; the canonical store's read side (CopyConversation)
// returns the flat []Message with the system prompt stored separately. The
// sync seam (message.SyncHistoryToConversation, issue #1235) is the bridge:
// it converts the history read into the canonical flat shape, stripping the
// leading system message — so both sides are compared in one canonical shape.

// fixedTime is the deterministic CreatedAt stamped on canonical-store appends
// (the history store stamps its own time.Now()); normalization zeroes the
// difference before comparison.
var fixedTime = mustParseTime()

func mustParseTime() (t time.Time) {
	t, err := time.Parse(time.RFC3339, "2026-01-02T15:04:05Z")
	if err != nil {
		panic(err)
	}
	return t
}

type parityPair struct {
	history *history.SessionManager
	session *session.Manager
	sessID  string
}

func newParityPair(t *testing.T, cap int) *parityPair {
	t.Helper()
	h := history.NewSessionManager(cap)
	p := &parityPair{
		history: h,
		session: session.NewManager(10, t.TempDir(), session.WithMaxExchanges(cap)),
	}
	h.Create("sess-1")
	sess, err := p.session.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}
	p.sessID = sess.ID
	return p
}

func (p *parityPair) appendUser(t *testing.T, content string) {
	t.Helper()
	p.history.AppendUser("sess-1", content)
	p.session.AppendMessage(p.sessID, message.Message{Role: "user", Content: content, CreatedAt: fixedTime})
}

func (p *parityPair) appendAssistant(t *testing.T, content string, toolCalls []message.ToolCall) {
	t.Helper()
	p.history.AppendAssistant("sess-1", content, toolCalls)
	p.session.AppendMessage(p.sessID, message.Message{Role: "assistant", Content: content, ToolCalls: toolCalls, CreatedAt: fixedTime})
}

func (p *parityPair) appendTool(t *testing.T, toolCallID, content string) {
	t.Helper()
	p.history.AppendTool("sess-1", toolCallID, content, "", false)
	p.session.AppendMessage(p.sessID, message.Message{Role: "tool", ToolCallID: toolCallID, Content: content, CreatedAt: fixedTime})
}

func (p *parityPair) repair(t *testing.T) {
	t.Helper()
	p.history.RepairPendingToolUse("sess-1")
	p.session.RepairPendingToolUse(p.sessID)
}

// sessionMessages returns the canonical store's conversation as flat messages.
func (p *parityPair) sessionMessages(t *testing.T) []message.Message {
	t.Helper()
	convo := p.session.CopyConversation(p.sessID)
	if convo == nil {
		t.Fatal("CopyConversation returned nil")
	}
	return convo.Messages
}

// historyMessages returns the history store's read converted to the canonical
// flat shape (system prompt stripped via the sync seam).
func (p *parityPair) historyMessages() []message.Message {
	return message.SyncHistoryToConversation(p.history.History("sess-1"))
}

// normalize zeroes the fields the two stores legitimately differ on — the
// stores stamp their own CreatedAt, and RawContent is only retained by the
// canonical store (the history read loses it in the litellm round-trip) —
// leaving the conversation content to be compared exactly.
func normalize(msgs []message.Message) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		out[i].CreatedAt = fixedTime
		out[i].RawContent = ""
	}
	return out
}

func assertConversationsEqual(t *testing.T, want, got []message.Message) {
	t.Helper()
	want = normalize(want)
	got = normalize(got)
	if len(want) != len(got) {
		t.Fatalf("conversation lengths differ: history=%d, session=%d\nhistory: %+v\nsession: %+v",
			len(want), len(got), want, got)
	}
	for i := range want {
		if want[i].Role != got[i].Role || want[i].Content != got[i].Content ||
			want[i].ToolCallID != got[i].ToolCallID || len(want[i].ToolCalls) != len(got[i].ToolCalls) {
			t.Errorf("message %d differs:\n history: %+v\n session: %+v", i, want[i], got[i])
			continue
		}
		for j := range want[i].ToolCalls {
			w, g := want[i].ToolCalls[j], got[i].ToolCalls[j]
			if w.ID != g.ID || w.Type != g.Type || w.Function.Name != g.Function.Name || w.Function.Arguments != g.Function.Arguments {
				t.Errorf("message %d tool call %d differs:\n history: %+v\n session: %+v", i, j, w, g)
			}
		}
	}
}

func TestParity_ExchangeCapTrimsIdentically(t *testing.T) {
	p := newParityPair(t, 3)

	// Four exchanges — the fourth must trigger the identical trim in both.
	for i := 0; i < 4; i++ {
		p.appendUser(t, "user-message")
		p.appendAssistant(t, "response", nil)
	}

	assertConversationsEqual(t, p.historyMessages(), p.sessionMessages(t))
	if userCount(p.sessionMessages(t)) > 3 {
		t.Errorf("session store user messages = %d, want <= 3", userCount(p.sessionMessages(t)))
	}
}

func TestParity_ExchangeCapWithToolMessages(t *testing.T) {
	p := newParityPair(t, 2)

	p.appendUser(t, "first")
	p.appendAssistant(t, "", []message.ToolCall{{ID: "call-1", Type: "function", Function: message.FunctionCall{Name: "file_viewer", Arguments: `{}`}}})
	p.appendTool(t, "call-1", "file contents")
	p.appendAssistant(t, "resp1", nil)

	p.appendUser(t, "second")
	p.appendAssistant(t, "", []message.ToolCall{{ID: "call-2", Type: "function", Function: message.FunctionCall{Name: "terminal_execute", Arguments: `{"cmd":"ls"}`}}})
	p.appendTool(t, "call-2", "output")

	p.appendUser(t, "third")
	p.appendAssistant(t, "resp3", nil)

	assertConversationsEqual(t, p.historyMessages(), p.sessionMessages(t))
}

func TestParity_DefaultCapResolvesIdentically(t *testing.T) {
	// Both stores must resolve the same default cap when handed a non-positive
	// value (history.NewSessionManager(0) and WithMaxExchanges(0) both → 150).
	h := history.NewSessionManager(0)
	h.Create("sess-1")
	s := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(0))
	sess, err := s.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	for range 160 {
		h.AppendUser("sess-1", "message")
		h.AppendAssistant("sess-1", "response", nil)
		s.AppendMessage(sess.ID, message.Message{Role: "user", Content: "message", CreatedAt: fixedTime})
		s.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "response", CreatedAt: fixedTime})
	}

	histMsgs := message.SyncHistoryToConversation(h.History("sess-1"))
	convMsgs := s.CopyConversation(sess.ID).Messages
	if userCount(histMsgs) != userCount(convMsgs) {
		t.Fatalf("default-cap user counts differ: history=%d, session=%d", userCount(histMsgs), userCount(convMsgs))
	}
	assertConversationsEqual(t, histMsgs, convMsgs)
}

func TestParity_RepairPendingToolUse(t *testing.T) {
	p := newParityPair(t, 10)

	p.appendUser(t, "do it")
	p.appendAssistant(t, "", []message.ToolCall{{ID: "call_123", Type: "function", Function: message.FunctionCall{Name: "browser", Arguments: `{"action":"navigate"}`}}})

	// Repair before the next user message — the canonical store must append
	// the same synthetic tool error result as the history store.
	p.repair(t)
	assertConversationsEqual(t, p.historyMessages(), p.sessionMessages(t))

	// A resolved tool call must be left unchanged in both.
	p.appendAssistant(t, "", []message.ToolCall{{ID: "call_456", Type: "function", Function: message.FunctionCall{Name: "bash", Arguments: `{}`}}})
	p.appendTool(t, "call_456", "ok")
	p.repair(t)
	assertConversationsEqual(t, p.historyMessages(), p.sessionMessages(t))
}
