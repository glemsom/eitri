package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/voocel/litellm"
)

// ── sessionHistoryManager tests ────────────────────────────────────────────

// addTestSession inserts a canonical-store session with the given ID and
// system prompt, mirroring the run-start contract: the loop's session-backed
// history adapter reads and writes the canonical conversation store directly
// (issue #1241), so tests seed the conversation there rather than in a
// separate history store.
func addTestSession(t *testing.T, mgr *uisession.Manager, id, systemPrompt string) {
	t.Helper()
	mgr.Add(&uisession.UISession{
		ID:        id,
		Title:     "test",
		Messages:  []message.Message{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	if systemPrompt != "" {
		mgr.SetSystemPrompt(id, systemPrompt)
	}
}

func TestSessionHistoryManager_History(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-hist"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hello", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	msgs := adapter.History()

	if len(msgs) == 0 {
		t.Fatal("History() returned empty slice")
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message role = %q, want %q", msgs[0].Role, "system")
	}
	if len(msgs) < 2 {
		t.Fatal("expected at least 2 messages (system + user)")
	}
	if msgs[len(msgs)-1].Role != "user" {
		t.Errorf("last message role = %q, want %q", msgs[len(msgs)-1].Role, "user")
	}
}

func TestSessionHistoryManager_History_DefaultSystemPromptFallback(t *testing.T) {
	t.Parallel()
	// A session whose system prompt was never set must fall back to the
	// canonical persona default, exactly like the history store.
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-dflt"
	addTestSession(t, mgr, sessionID, "")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hi", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	msgs := adapter.History()
	if len(msgs) != 2 {
		t.Fatalf("History() returned %d messages, want 2", len(msgs))
	}
	if got := msgs[0].Content(); got != history.DefaultSystemPrompt {
		t.Errorf("system message content = %q, want %q", got, history.DefaultSystemPrompt)
	}
}

func TestSessionHistoryManager_History_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := NewSessionHistoryManager(nil, "test-session")
	msgs := adapter.History()
	if msgs != nil {
		t.Errorf("History() = %v, want nil when sessionMgr is nil", msgs)
	}
}

func TestSessionHistoryManager_History_UnknownSession(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	adapter := NewSessionHistoryManager(mgr, "unknown-session")
	if msgs := adapter.History(); msgs != nil {
		t.Errorf("History() = %v, want nil for unknown session", msgs)
	}
}

// TestSessionHistoryManager_History_ConcurrentWithAppend is a race-regression
// test for issue #1241's fix round: History() must read the canonical
// conversation through a locked copy accessor, never by iterating the live
// shared reference that the run goroutine keeps appending to. Manual
// compaction (POST /api/sessions/{id}/compact) calls History() with no
// active-run guard, so the reader (this goroutine) and the writer (the
// simulated active run's AppendAssistant/AppendTool) overlap in production.
// Under -race this test must report no data races; before the fix it fails
// with a DATA RACE on the shared conversation's slice header and elements.
func TestSessionHistoryManager_History_ConcurrentWithAppend(t *testing.T) {
	mgr := uisession.NewManager(100, t.TempDir())
	sessionID := "race-session-hist"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "start", CreatedAt: time.Now()})
	adapter := NewSessionHistoryManager(mgr, sessionID)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	// Simulated active run goroutine: keeps appending assistant/tool messages
	// via the adapter, exactly what the agent loop does mid-run (issue #1241).
	// A user message every 10 appends keeps the exchange-cap trim active so
	// the conversation stays bounded for the whole overlap window.
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			adapter.AppendAssistant(fmt.Sprintf("reply %d", i), nil)
			adapter.AppendTool(fmt.Sprintf("call_%d", i), "result", "", false)
			if i%10 == 0 {
				mgr.AppendToConversation(sessionID, message.Message{
					Role: "user", Content: fmt.Sprintf("user %d", i), CreatedAt: time.Now(),
				})
			}
			i++
		}
	}()

	// Simulated manual-compaction goroutine: reads the full history with no
	// active-run guard (handleCompact), overlapping the run's appends.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			msgs := adapter.History()
			if msgs != nil {
				// Walk the result so element reads are exercised.
				for _, m := range msgs {
					_ = m.Role
					_ = m.Content()
				}
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	// The conversation must still read coherently after the overlap.
	if got := adapter.History(); got == nil || len(got) < 2 {
		t.Fatalf("History() = %v, want >= 2 messages (system + user)", got)
	}
}

func TestSessionHistoryManager_AppendAssistant(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-aa"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hi", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	adapter.AppendAssistant("Hello!", nil)

	msgs := adapter.History()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want %q", last.Role, "assistant")
	}
	if last.Content() != "Hello!" {
		t.Errorf("last message content = %q, want %q", last.Content(), "Hello!")
	}
}

func TestSessionHistoryManager_AppendAssistant_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := NewSessionHistoryManager(nil, "test-session")
	// Should not panic
	adapter.AppendAssistant("Hello!", nil)
}

func TestSessionHistoryManager_AppendAssistant_SkipsEmpty(t *testing.T) {
	t.Parallel()
	// Empty assistant messages serialise as {"role":"assistant"} with no
	// content or tool calls, which some providers reject. The session-backed
	// adapter must skip them exactly like the old history store did so the
	// LLM request history stays byte-identical (issue #1241).
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-empty"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hi", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	adapter.AppendAssistant("", nil)

	msgs := adapter.History()
	if len(msgs) != 2 {
		t.Fatalf("History() has %d messages, want 2 (system + user; empty assistant skipped)", len(msgs))
	}
}

// TestSessionHistoryManager_History_FiltersEmptyAssistantPlaceholders guards
// AC2 for realistic UI runs: session.Manager.AppendComponent/SetQuickReplies
// append a bare empty assistant placeholder ({Role:"assistant", Content:""})
// into the canonical store whenever the last conversation message is not an
// assistant message. In the loop's tool-execution path component emission runs
// *before* AppendTool, so the second and subsequent component-emitting tool
// calls of a turn each create such a placeholder after the previous tool
// result. The old LLM-history store never carried these UI-only placeholders,
// so the session-backed adapter's History() must filter them on read — left
// unfiltered they reach the next LLM request as {"role":"assistant"} with no
// content or tool_calls, which some providers reject.
func TestSessionHistoryManager_History_FiltersEmptyAssistantPlaceholders(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-placeholder"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "render two diagrams", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)

	// A turn with two component-emitting tool calls, driven exactly like the
	// loop: AppendAssistant (assistant with tool calls), then per tool —
	// AppendComponent *before* AppendTool. The first component attaches to the
	// assistant message (last is assistant); the second runs after the first
	// tool result, so AppendComponent creates an empty assistant placeholder.
	adapter.AppendAssistant("", []litellm.ToolUseBlock{
		{ID: "call_1", Name: "render_mermaid_diagram", Arguments: json.RawMessage(`{"code":"graph TD; A-->B;"}`)},
		{ID: "call_2", Name: "render_mermaid_diagram", Arguments: json.RawMessage(`{"code":"graph TD; C-->D;"}`)},
	})
	_ = mgr.AppendComponent(sessionID, message.ComponentData{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A-->B;"}})
	adapter.AppendTool("call_1", "rendered", "", false)
	_ = mgr.AppendComponent(sessionID, message.ComponentData{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; C-->D;"}})
	adapter.AppendTool("call_2", "rendered", "", false)
	// SetQuickReplies after a tool result creates a placeholder too.
	_ = mgr.SetQuickReplies(sessionID, []string{"yes", "no"})

	// The canonical store must retain the placeholders — they are the UI
	// component targets, and History() filters on read, never on write.
	convo := mgr.GetConversationShared(sessionID)
	placeholderCount := 0
	for _, msg := range convo.Messages {
		if msg.Role == "assistant" && msg.Content == "" && len(msg.ToolCalls) == 0 {
			placeholderCount++
		}
	}
	if placeholderCount == 0 {
		t.Fatal("test setup: expected empty assistant placeholders in the canonical store")
	}

	// The LLM request history must not carry them: every assistant message in
	// History() must have content or tool calls.
	hist := adapter.History()
	for i, em := range hist {
		if em.Role != litellm.Role("assistant") {
			continue
		}
		if em.Content() == "" && len(em.ToolCalls()) == 0 {
			t.Errorf("History()[%d]: empty assistant placeholder leaked into LLM history (%+v)", i, em)
		}
	}
	// Exact shape after the filtering: system + user + assistant(tool calls) +
	// the two tool results — no placeholder messages.
	if len(hist) != 5 {
		t.Fatalf("History() has %d messages, want 5 (system, user, assistant, tool, tool)", len(hist))
	}
	if got := []string{string(hist[0].Role), string(hist[1].Role), string(hist[2].Role), string(hist[3].Role), string(hist[4].Role)}; !slices.Equal(got, []string{"system", "user", "assistant", "tool", "tool"}) {
		t.Errorf("History() roles = %v, want [system user assistant tool tool]", got)
	}
}

func TestSessionHistoryManager_AppendAssistantWithToolCalls(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-tc"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "run tool", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	toolCalls := []litellm.ToolUseBlock{
		{ID: "call_1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
	}
	adapter.AppendAssistant("", toolCalls)

	msgs := adapter.History()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want %q", last.Role, "assistant")
	}
	if len(last.ToolCalls()) != 1 {
		t.Fatalf("last message has %d tool calls, want 1", len(last.ToolCalls()))
	}
	if last.ToolCalls()[0].Function.Name != "test_tool" {
		t.Errorf("tool call name = %q, want %q", last.ToolCalls()[0].Function.Name, "test_tool")
	}
}

func TestSessionHistoryManager_AppendTool(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-at"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "run tool", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	adapter.AppendTool("call_1", "result content", "", false)

	msgs := adapter.History()
	last := msgs[len(msgs)-1]
	if last.Role != "tool" {
		t.Errorf("last message role = %q, want %q", last.Role, "tool")
	}
	if last.Content() != "result content" {
		t.Errorf("last message content = %q, want %q", last.Content(), "result content")
	}
	if last.ToolCallID() != "call_1" {
		t.Errorf("last message ToolCallID = %q, want %q", last.ToolCallID(), "call_1")
	}
}

func TestSessionHistoryManager_AppendTool_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := NewSessionHistoryManager(nil, "test-session")
	// Should not panic
	adapter.AppendTool("call_1", "result", "", false)
}

func TestSessionHistoryManager_ReplaceHistory(t *testing.T) {
	t.Parallel()
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-rh"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hello", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	adapter.ReplaceHistory([]message.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "compacted user"},
		{Role: "assistant", Content: "compacted answer"},
	})

	msgs := adapter.History()
	if len(msgs) != 3 {
		t.Fatalf("History() returned %d messages, want 3", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content() != "You are helpful." {
		t.Errorf("message[0] = %q/%q, want system prompt preserved", msgs[0].Role, msgs[0].Content())
	}
	if msgs[2].Content() != "compacted answer" {
		t.Errorf("message[2] content = %q, want %q", msgs[2].Content(), "compacted answer")
	}
	// The leading system message must be extracted into the canonical store's
	// separate SystemPrompt field, never left in the conversation messages
	// (the strip-system-message invariant, ADR-0028).
	convo := mgr.GetConversationShared(sessionID)
	if convo == nil || len(convo.Messages) != 2 {
		t.Fatalf("canonical conversation = %+v, want 2 messages (system stripped)", convo)
	}
	if convo.SystemPrompt != "You are helpful." {
		t.Errorf("canonical SystemPrompt = %q, want %q", convo.SystemPrompt, "You are helpful.")
	}
}

func TestSessionHistoryManager_ReplaceHistory_NoSystemMessage(t *testing.T) {
	t.Parallel()
	// ReplaceHistory without a leading system message replaces the messages
	// verbatim and leaves the stored system prompt untouched (matches
	// history.SessionManager.RestoreHistory).
	mgr := uisession.NewManager(10, t.TempDir())
	sessionID := "test-session-rh2"
	addTestSession(t, mgr, sessionID, "You are helpful.")
	mgr.AppendToConversation(sessionID, message.Message{Role: "user", Content: "hello", CreatedAt: time.Now()})

	adapter := NewSessionHistoryManager(mgr, sessionID)
	adapter.ReplaceHistory([]message.Message{
		{Role: "user", Content: "compacted user"},
		{Role: "assistant", Content: "compacted answer"},
	})

	msgs := adapter.History()
	if len(msgs) != 3 {
		t.Fatalf("History() returned %d messages, want 3", len(msgs))
	}
	if msgs[0].Content() != "You are helpful." {
		t.Errorf("system prompt = %q, want %q (unchanged)", msgs[0].Content(), "You are helpful.")
	}
}

func TestSessionHistoryManager_ReplaceHistory_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := NewSessionHistoryManager(nil, "test-session")
	// Should not panic
	adapter.ReplaceHistory([]message.Message{{Role: "user", Content: "x"}})
}

// TestSessionHistoryManager_ParityWithHistoryStore verifies the acceptance
// criterion that the session-backed adapter produces byte-identical LLM
// request history after pointing it at the canonical store (issue #1241):
// the same operation sequence driven through the canonical store (via the
// adapter) and through the old LLM-history store yields identical litellm
// messages (the system prompt + every turn's assistant/tool appends).
func TestSessionHistoryManager_ParityWithHistoryStore(t *testing.T) {
	t.Parallel()
	const (
		id      = "parity-session"
		sysPrmt = "You are Eitri."
	)

	// Old store: driven through history.SessionManager's own API.
	oldStore := history.NewSessionManager(50)
	oldStore.Create(id)
	oldStore.SetSystemPrompt(id, sysPrmt)
	oldStore.AppendUser(id, "run the build")

	// Canonical store: driven through the session-backed adapter (issue #1241).
	newMgr := uisession.NewManager(10, t.TempDir())
	newMgr.Add(&uisession.UISession{
		ID:        id,
		Title:     "test",
		Messages:  []message.Message{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	newMgr.SetSystemPrompt(id, sysPrmt)
	newMgr.AppendToConversation(id, message.Message{Role: "user", Content: "run the build", CreatedAt: time.Now()})
	adapter := NewSessionHistoryManager(newMgr, id)

	// The same turn sequence through both conversation sources.
	oldStore.AppendAssistant(id, "let me check", nil)
	adapter.AppendAssistant("let me check", nil)

	oldStore.AppendTool(id, "call_1", "result content", "", false)
	adapter.AppendTool("call_1", "result content", "", false)

	oldStore.AppendAssistant(id, "", []message.ToolCall{
		{ID: "call_2", Type: "function", Function: message.FunctionCall{Name: "test_tool", Arguments: `{}`}},
	})
	adapter.AppendAssistant("", []litellm.ToolUseBlock{
		{ID: "call_2", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
	})

	oldStore.AppendTool(id, "call_2", "done", "", false)
	adapter.AppendTool("call_2", "done", "", false)

	oldStore.AppendAssistant(id, "build finished", nil)
	adapter.AppendAssistant("build finished", nil)

	oldHist := oldStore.History(id)
	newHist := adapter.History()
	if len(oldHist) != len(newHist) {
		t.Fatalf("history lengths differ: old=%d new=%d", len(oldHist), len(newHist))
	}
	for i := range oldHist {
		oldMsg := oldHist[i].ToLitellm()
		newMsg := newHist[i].ToLitellm()
		if oldMsg.Role != newMsg.Role {
			t.Errorf("message[%d] role: old=%q new=%q", i, oldMsg.Role, newMsg.Role)
			continue
		}
		oldJSON, _ := json.Marshal(oldMsg)
		newJSON, _ := json.Marshal(newMsg)
		if string(oldJSON) != string(newJSON) {
			t.Errorf("message[%d] serialized litellm differs:\n old=%s\n new=%s", i, oldJSON, newJSON)
		}
	}
}

func TestSessionHistoryManager_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time interface check: *sessionHistoryManager must satisfy HistoryManager
	var _ HistoryManager = (*sessionHistoryManager)(nil)
}

// ── requestHistoryManager tests ────────────────────────────────────────────

func TestRequestHistoryManager_History(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
		},
	}
	adapter := NewRequestHistoryManager(req)
	msgs := adapter.History()

	if len(msgs) != 2 {
		t.Fatalf("History() returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Content() != "sys" {
		t.Errorf("message[0].Content() = %q, want %q", msgs[0].Content(), "sys")
	}
	if msgs[1].Content() != "hello" {
		t.Errorf("message[1].Content() = %q, want %q", msgs[1].Content(), "hello")
	}
}

func TestRequestHistoryManager_HistoryReturnsNewSlice(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
	}
	adapter := NewRequestHistoryManager(req)
	msgs := adapter.History()
	// History() returns a new slice of EitriMessage values (converted from litellm).
	// Modifications to the returned slice should NOT affect req.Messages.
	if len(msgs) > 0 {
		msgs[0] = message.EitriMessage{
			Message: litellm.Message{
				Role:   litellm.Role("user"),
				Blocks: []litellm.Block{litellm.TextBlock{Text: "modified"}},
			},
		}
	}
	// Extract content from first message in req.Messages
	firstBlocks := req.Messages[0].Blocks
	firstContent := ""
	if len(firstBlocks) > 0 {
		if text, ok := firstBlocks[0].(litellm.TextBlock); ok {
			firstContent = text.Text
		}
	}
	if firstContent != "hi" {
		t.Errorf("req.Messages[0] content = %q, want %q (History should not share the backing slice)", firstContent, "hi")
	}
}

func TestRequestHistoryManager_AppendAssistant(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
		},
	}
	adapter := NewRequestHistoryManager(req)
	adapter.AppendAssistant("world", nil)

	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[1].Role != litellm.Role("assistant") {
		t.Errorf("message[1].Role = %q, want %q", req.Messages[1].Role, litellm.Role("assistant"))
	}
	// Extract text from blocks
	if len(req.Messages[1].Blocks) > 0 {
		if text, ok := req.Messages[1].Blocks[0].(litellm.TextBlock); ok {
			if text.Text != "world" {
				t.Errorf("message[1] text = %q, want %q", text.Text, "world")
			}
		}
	}
}

func TestRequestHistoryManager_AppendAssistantWithToolCalls(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{}
	adapter := NewRequestHistoryManager(req)
	toolCalls := []litellm.ToolUseBlock{
		{ID: "call_1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
	}
	adapter.AppendAssistant("", toolCalls)

	if len(req.Messages) != 1 {
		t.Fatalf("req.Messages length = %d, want 1", len(req.Messages))
	}
	// Verify the message has a ToolUseBlock
	foundTool := false
	for _, block := range req.Messages[0].Blocks {
		if tu, ok := block.(litellm.ToolUseBlock); ok {
			foundTool = true
			if tu.Name != "test_tool" {
				t.Errorf("ToolUseBlock.Name = %q, want %q", tu.Name, "test_tool")
			}
			break
		}
	}
	if !foundTool {
		t.Error("no ToolUseBlock found in appended message")
	}
}

func TestRequestHistoryManager_AppendTool(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run tool"}}},
		},
	}
	adapter := NewRequestHistoryManager(req)
	adapter.AppendTool("call_1", "tool result", "", false)

	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[1].Role != litellm.Role("tool") {
		t.Errorf("message[1].Role = %q, want %q", req.Messages[1].Role, litellm.Role("tool"))
	}
	// Verify ToolResultBlock
	if len(req.Messages[1].Blocks) > 0 {
		if tr, ok := req.Messages[1].Blocks[0].(litellm.ToolResultBlock); ok {
			if tr.ToolUseID != "call_1" {
				t.Errorf("ToolResultBlock.ToolUseID = %q, want %q", tr.ToolUseID, "call_1")
			}
		}
	}
}

func TestRequestHistoryManager_AppendToolErrorFlag(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{}
	adapter := NewRequestHistoryManager(req)
	// isError is not stored in litellm message, but the content carries the error info.
	adapter.AppendTool("call_err", "error message", "", true)

	if len(req.Messages) != 1 {
		t.Fatalf("req.Messages length = %d, want 1", len(req.Messages))
	}
	// Content should be "error message"
	_ = req.Messages[0].Blocks
	// The isError flag is intentionally discarded by requestHistoryManager
	// because the message type does not carry it; the error content
	// is passed in the Content field for the LLM to interpret.
}

func TestRequestHistoryManager_ReplaceHistory(t *testing.T) {
	t.Parallel()
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
			{Role: litellm.Role("tool"), Blocks: []litellm.Block{litellm.TextBlock{Text: "huge result"}}},
		},
	}
	adapter := NewRequestHistoryManager(req)
	adapter.ReplaceHistory([]message.Message{
		{Role: "system", Content: "sys"},
		{Role: "tool", ToolCallID: "call_1", Content: "[TOOL RESULT COMPACTED - originally 1000 tokens] summary"},
	})

	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != litellm.Role("system") {
		t.Errorf("message[0].Role = %q, want %q", req.Messages[0].Role, litellm.Role("system"))
	}
	// The tool message must round-trip through the flat Message type back into
	// a ToolResultBlock so the LLM sees a well-formed tool result.
	tr, ok := req.Messages[1].Blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("message[1].Blocks[0] = %T, want litellm.ToolResultBlock", req.Messages[1].Blocks[0])
	}
	if tr.ToolUseID != "call_1" {
		t.Errorf("ToolResultBlock.ToolUseID = %q, want %q", tr.ToolUseID, "call_1")
	}
	if len(tr.Content) == 0 {
		t.Fatal("ToolResultBlock has no content blocks")
	}
	if tb, ok := tr.Content[0].(litellm.TextBlock); !ok || !strings.Contains(tb.Text, "[TOOL RESULT COMPACTED") {
		t.Errorf("ToolResultBlock content = %v, want compacted marker", tr.Content)
	}
}

func TestRequestHistoryManager_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time interface check: *requestHistoryManager must satisfy HistoryManager
	var _ HistoryManager = (*requestHistoryManager)(nil)
}

// ── testConfirmerStub tests ────────────────────────────────────────────────

func TestTestConfirmerStub_ConfirmApproved(t *testing.T) {
	t.Parallel()
	expected := &ConfirmationResult{Path: "/tmp/test", Approved: true}
	stub := NewTestConfirmerStub(expected, nil)

	result, err := stub.Confirm(context.Background(), "session-1", "/tmp/test", "Allow?")
	if err != nil {
		t.Fatalf("Confirm error: %v", err)
	}
	if result.Path != expected.Path {
		t.Errorf("result.Path = %q, want %q", result.Path, expected.Path)
	}
	if result.Approved != expected.Approved {
		t.Errorf("result.Approved = %t, want %t", result.Approved, expected.Approved)
	}
}

func TestTestConfirmerStub_ConfirmDenied(t *testing.T) {
	t.Parallel()
	expected := &ConfirmationResult{Path: "/tmp/test", Approved: false}
	stub := NewTestConfirmerStub(expected, nil)

	result, err := stub.Confirm(context.Background(), "session-1", "/tmp/test", "Allow?")
	if err != nil {
		t.Fatalf("Confirm error: %v", err)
	}
	if result.Approved != false {
		t.Errorf("result.Approved = %t, want false", result.Approved)
	}
}

func TestTestConfirmerStub_ConfirmError(t *testing.T) {
	t.Parallel()
	stub := NewTestConfirmerStub(nil, errors.New("stub error"))

	result, err := stub.Confirm(context.Background(), "session-1", "/path", "msg")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestTestConfirmerStub_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time interface check: *testConfirmerStub must satisfy Confirmer
	var _ Confirmer = (*testConfirmerStub)(nil)
}
