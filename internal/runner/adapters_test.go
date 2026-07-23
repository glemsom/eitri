package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/llm"
)

// ── sessionHistoryManager tests ────────────────────────────────────────────

func TestSessionHistoryManager_History(t *testing.T) {
	t.Parallel()
	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-hist"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "hello")

	adapter := newSessionHistoryManager(sessionMgr, nil, sessionID)
	msgs := adapter.History(sessionID)

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

func TestSessionHistoryManager_History_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := newSessionHistoryManager(nil, nil, "test-session")
	msgs := adapter.History("test-session")
	if msgs != nil {
		t.Errorf("History() = %v, want nil when sessionMgr is nil", msgs)
	}
}

func TestSessionHistoryManager_AppendAssistant(t *testing.T) {
	t.Parallel()
	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-aa"
	sessionMgr.Create(sessionID)
	sessionMgr.AppendUser(sessionID, "hi")

	adapter := newSessionHistoryManager(sessionMgr, nil, sessionID)
	adapter.AppendAssistant(sessionID, "Hello!", nil)

	msgs := adapter.History(sessionID)
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want %q", last.Role, "assistant")
	}
	if last.Content != "Hello!" {
		t.Errorf("last message content = %q, want %q", last.Content, "Hello!")
	}
}

func TestSessionHistoryManager_AppendAssistant_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := newSessionHistoryManager(nil, nil, "test-session")
	// Should not panic
	adapter.AppendAssistant("test-session", "Hello!", nil)
}

func TestSessionHistoryManager_AppendAssistantWithToolCalls(t *testing.T) {
	t.Parallel()
	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-tc"
	sessionMgr.Create(sessionID)
	sessionMgr.AppendUser(sessionID, "run tool")

	adapter := newSessionHistoryManager(sessionMgr, nil, sessionID)
	toolCalls := []llm.ToolCall{
		{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "test_tool", Arguments: `{}`}},
	}
	adapter.AppendAssistant(sessionID, "", toolCalls)

	msgs := adapter.History(sessionID)
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want %q", last.Role, "assistant")
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("last message has %d tool calls, want 1", len(last.ToolCalls))
	}
	if last.ToolCalls[0].Function.Name != "test_tool" {
		t.Errorf("tool call name = %q, want %q", last.ToolCalls[0].Function.Name, "test_tool")
	}
}

func TestSessionHistoryManager_AppendTool(t *testing.T) {
	t.Parallel()
	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-at"
	sessionMgr.Create(sessionID)
	sessionMgr.AppendUser(sessionID, "run tool")

	adapter := newSessionHistoryManager(sessionMgr, nil, sessionID)
	adapter.AppendTool(sessionID, "call_1", "result content", false)

	msgs := adapter.History(sessionID)
	last := msgs[len(msgs)-1]
	if last.Role != "tool" {
		t.Errorf("last message role = %q, want %q", last.Role, "tool")
	}
	if last.Content != "result content" {
		t.Errorf("last message content = %q, want %q", last.Content, "result content")
	}
	if last.ToolCallID != "call_1" {
		t.Errorf("last message ToolCallID = %q, want %q", last.ToolCallID, "call_1")
	}
}

func TestSessionHistoryManager_AppendTool_NilSessionMgr(t *testing.T) {
	t.Parallel()
	adapter := newSessionHistoryManager(nil, nil, "test-session")
	// Should not panic
	adapter.AppendTool("test-session", "call_1", "result", false)
}

func TestSessionHistoryManager_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time interface check: *sessionHistoryManager must satisfy HistoryManager
	var _ HistoryManager = (*sessionHistoryManager)(nil)
}

// ── requestHistoryManager tests ────────────────────────────────────────────

func TestRequestHistoryManager_History(t *testing.T) {
	t.Parallel()
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
	}
	adapter := newRequestHistoryManager(req)
	msgs := adapter.History("")

	if len(msgs) != 2 {
		t.Fatalf("History() returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Content != "sys" {
		t.Errorf("message[0].Content = %q, want %q", msgs[0].Content, "sys")
	}
	if msgs[1].Content != "hello" {
		t.Errorf("message[1].Content = %q, want %q", msgs[1].Content, "hello")
	}
}

func TestRequestHistoryManager_HistoryReturnsSameSlice(t *testing.T) {
	t.Parallel()
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "hi"},
		},
	}
	adapter := newRequestHistoryManager(req)
	msgs := adapter.History("")
	// Modify the returned slice — should affect req.Messages since no copy is made
	if len(msgs) > 0 {
		msgs[0].Content = "modified"
	}
	if req.Messages[0].Content != "modified" {
		t.Error("History() did not return the same backing slice as req.Messages")
	}
}

func TestRequestHistoryManager_AppendAssistant(t *testing.T) {
	t.Parallel()
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	}
	adapter := newRequestHistoryManager(req)
	adapter.AppendAssistant("", "world", nil)

	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("message[1].Role = %q, want %q", req.Messages[1].Role, "assistant")
	}
	if req.Messages[1].Content != "world" {
		t.Errorf("message[1].Content = %q, want %q", req.Messages[1].Content, "world")
	}
}

func TestRequestHistoryManager_AppendAssistantWithToolCalls(t *testing.T) {
	t.Parallel()
	req := &llm.Request{}
	adapter := newRequestHistoryManager(req)
	toolCalls := []llm.ToolCall{
		{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "test_tool", Arguments: `{}`}},
	}
	adapter.AppendAssistant("", "", toolCalls)

	if len(req.Messages) != 1 {
		t.Fatalf("req.Messages length = %d, want 1", len(req.Messages))
	}
	if len(req.Messages[0].ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(req.Messages[0].ToolCalls))
	}
	if req.Messages[0].ToolCalls[0].Function.Name != "test_tool" {
		t.Errorf("ToolCalls[0].Function.Name = %q, want %q", req.Messages[0].ToolCalls[0].Function.Name, "test_tool")
	}
}

func TestRequestHistoryManager_AppendTool(t *testing.T) {
	t.Parallel()
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: "run tool"},
		},
	}
	adapter := newRequestHistoryManager(req)
	adapter.AppendTool("", "call_1", "tool result", false)

	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[1].Role != "tool" {
		t.Errorf("message[1].Role = %q, want %q", req.Messages[1].Role, "tool")
	}
	if req.Messages[1].ToolCallID != "call_1" {
		t.Errorf("message[1].ToolCallID = %q, want %q", req.Messages[1].ToolCallID, "call_1")
	}
	if req.Messages[1].Content != "tool result" {
		t.Errorf("message[1].Content = %q, want %q", req.Messages[1].Content, "tool result")
	}
}

func TestRequestHistoryManager_AppendToolErrorFlag(t *testing.T) {
	t.Parallel()
	req := &llm.Request{}
	adapter := newRequestHistoryManager(req)
	// isError is not stored in llm.Message, but the content carries the error info.
	adapter.AppendTool("", "call_err", "error message", true)

	if len(req.Messages) != 1 {
		t.Fatalf("req.Messages length = %d, want 1", len(req.Messages))
	}
	_ = req.Messages[0].Content // Content should be "error message"
	// The isError flag is intentionally discarded by requestHistoryManager
	// because the llm.Message type does not carry it; the error content
	// is passed in the Content field for the LLM to interpret.
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
	stub := newTestConfirmerStub(expected, nil)

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
	stub := newTestConfirmerStub(expected, nil)

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
	stub := newTestConfirmerStub(nil, errors.New("stub error"))

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
