package session_test

import (
	"testing"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/session"
)

// messages returns the session's conversation messages as a detached copy.
func messages(t *testing.T, mgr *session.Manager, id string) []message.Message {
	t.Helper()
	convo := mgr.CopyConversation(id)
	if convo == nil {
		t.Fatalf("CopyConversation(%q) returned nil", id)
	}
	return convo.Messages
}

func TestManager_ExchangeCapTrimsOldestFirst(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(3))
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "msg"})
		mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "resp"})
	}

	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) > 3 {
		t.Fatalf("user messages after 4 exchanges with cap 3 = %d, want <= 3", userCount(msgs))
	}
	if userCount(msgs) != 3 {
		t.Errorf("user messages = %d, want exactly 3 (window keeps cap exchanges)", userCount(msgs))
	}
}

func TestManager_ExchangeCapDefault(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir()) // default cap 150
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	for range 160 {
		mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "message"})
		mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "response"})
	}

	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) > 150 {
		t.Errorf("user messages after 160 appends = %d, want <= 150", userCount(msgs))
	}
}

func TestManager_ExchangeCapWithToolMessages(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(2))
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	// Exchange 1: user -> assistant (tool call) -> tool result -> assistant final
	mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "first"})
	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", ToolCalls: []message.ToolCall{{ID: "call-1", Type: "function", Function: message.FunctionCall{Name: "file_viewer", Arguments: `{}`}}}})
	mgr.AppendMessage(sess.ID, message.Message{Role: "tool", ToolCallID: "call-1", Content: "content"})
	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "resp1"})

	// Exchange 2: user -> assistant
	mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "second"})
	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "resp2"})

	// Exchange 3: user triggers the trim
	mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "third"})
	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "resp3"})

	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) > 2 {
		t.Errorf("user messages = %d, want <= 2", userCount(msgs))
	}
	for _, m := range msgs {
		if m.Content == "first" {
			t.Error("trimmed user message 'first' still present")
		}
	}
}

func TestManager_ExchangeCapAppliesToAppendToConversation(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(1))
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		mgr.AppendToConversation(sess.ID, message.Message{Role: "user", Content: "q"})
	}

	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) != 1 {
		t.Fatalf("user messages after 3 appends with cap 1 = %d, want 1", userCount(msgs))
	}
}

func TestManager_ExchangeCapDoesNotTrimReplace(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(1))
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	// ReplaceConversationMessages is the canonical store's RestoreHistory
	// equivalent (compaction / per-turn live-sync write-back); like the
	// session-backed adapter's ReplaceHistory it must NOT trim.
	overCap := make([]message.Message, 0, 5)
	for i := 0; i < 5; i++ {
		overCap = append(overCap, message.Message{Role: "user", Content: "q"})
	}
	mgr.ReplaceConversationMessages(sess.ID, overCap)

	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) != 5 {
		t.Fatalf("ReplaceConversationMessages trimmed an over-cap list: user messages = %d, want 5", userCount(msgs))
	}
}

func TestManager_RepairPendingToolUse_ClosesTrailingAssistantToolCall(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir())
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "do it"})
	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", ToolCalls: []message.ToolCall{{ID: "call_123", Type: "function", Function: message.FunctionCall{Name: "browser", Arguments: `{"action":"navigate"}`}}}})

	mgr.RepairPendingToolUse(sess.ID)

	msgs := messages(t, mgr, sess.ID)
	if len(msgs) != 3 {
		t.Fatalf("messages after repair = %d, want 3 (synthetic error appended)", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" {
		t.Fatalf("last message role = %q, want %q", last.Role, "tool")
	}
	if last.ToolCallID != "call_123" {
		t.Errorf("last tool_call_id = %q, want %q", last.ToolCallID, "call_123")
	}
	if last.Content == "" {
		t.Error("synthetic tool error has empty content")
	}
}

func TestManager_RepairPendingToolUse_ResolvedToolCallUnchanged(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir())
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", ToolCalls: []message.ToolCall{{ID: "call_1", Type: "function", Function: message.FunctionCall{Name: "bash", Arguments: `{}`}}}})
	mgr.AppendMessage(sess.ID, message.Message{Role: "tool", ToolCallID: "call_1", Content: "hi"})

	mgr.RepairPendingToolUse(sess.ID)

	msgs := messages(t, mgr, sess.ID)
	if len(msgs) != 2 {
		t.Fatalf("RepairPendingToolUse changed a resolved tool call: %d -> %d messages", 2, len(msgs))
	}
}

func TestManager_RepairPendingToolUse_UnknownSessionNoop(t *testing.T) {
	mgr := session.NewManager(10, t.TempDir())
	mgr.RepairPendingToolUse("nonexistent") // must not panic
}

func TestManager_ExchangeCapConstructorNormalizesNonPositive(t *testing.T) {
	// WithMaxExchanges(<=0) must fall back to the default cap
	// (message.DefaultMaxExchanges).
	mgr := session.NewManager(10, t.TempDir(), session.WithMaxExchanges(0))
	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}
	for range 160 {
		mgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "message"})
		mgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "response"})
	}
	msgs := messages(t, mgr, sess.ID)
	if userCount(msgs) > 150 {
		t.Errorf("user messages after 160 appends = %d, want <= 150 (default cap)", userCount(msgs))
	}
}

func userCount(msgs []message.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "user" {
			n++
		}
	}
	return n
}
