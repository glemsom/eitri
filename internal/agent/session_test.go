package agent

import (
	"testing"

	"github.com/voocel/litellm"
)

func TestSessionManager_CreateAndGet(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	if !m.Get("sess-1") {
		t.Error("Get returned false for existing session")
	}
}

func TestSessionManager_GetUnknown(t *testing.T) {
	m := NewSessionManager(50)

	if m.Get("nonexistent") {
		t.Error("Get returned true for unknown session")
	}
}

func TestSessionManager_AppendUser(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	m.AppendUser("sess-1", "hello")

	history := m.History("sess-1")
	if len(history) != 2 { // system prompt + user message
		t.Fatalf("History length = %d, want 2", len(history))
	}
	if history[0].Role != litellm.RoleSystem {
		t.Errorf("First message role = %q, want %q", history[0].Role, litellm.RoleSystem)
	}
	if history[1].Role != litellm.RoleUser {
		t.Errorf("Second message role = %q, want %q", history[1].Role, litellm.RoleUser)
	}
	if len(history[1].Blocks) != 1 {
		t.Fatalf("User blocks count = %d, want 1", len(history[1].Blocks))
	}
	text, ok := history[1].Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("User block type = %T, want TextBlock", history[1].Blocks[0])
	}
	if text.Text != "hello" {
		t.Errorf("User text = %q, want %q", text.Text, "hello")
	}
}

func TestSessionManager_AppendAssistant(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	blocks := []litellm.Block{litellm.TextBlock{Text: "Hi there!"}}
	toolCalls := []litellm.ToolUseBlock{
		{ID: "call-1", Name: "file_viewer", Arguments: []byte(`{"path":"test.txt"}`)},
	}

	m.AppendAssistant("sess-1", blocks, toolCalls)
	m.AppendUser("sess-1", "hello") // user to have something in front

	history := m.History("sess-1")
	// Find the assistant message
	var assistantMsg *litellm.Message
	for i := range history {
		if history[i].Role == litellm.RoleAssistant {
			assistantMsg = &history[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("No assistant message found in history")
	}
	if len(assistantMsg.Blocks) != 2 {
		t.Fatalf("Assistant blocks count = %d, want 2 (text + tool call)", len(assistantMsg.Blocks))
	}
	text, ok := assistantMsg.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("First block type = %T, want TextBlock", assistantMsg.Blocks[0])
	}
	if text.Text != "Hi there!" {
		t.Errorf("Assistant text = %q, want %q", text.Text, "Hi there!")
	}
	toolCall, ok := assistantMsg.Blocks[1].(litellm.ToolUseBlock)
	if !ok {
		t.Fatalf("Second block type = %T, want ToolUseBlock", assistantMsg.Blocks[1])
	}
	if toolCall.ID != "call-1" {
		t.Errorf("Tool call ID = %q, want %q", toolCall.ID, "call-1")
	}
}

func TestSessionManager_AppendTool(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	blocks := []litellm.Block{litellm.TextBlock{Text: "file contents"}}
	m.AppendTool("sess-1", "call-1", blocks, false)

	m.AppendUser("sess-1", "hello") // push a user to trigger history read

	history := m.History("sess-1")
	var toolMsg *litellm.Message
	for i := range history {
		if history[i].Role == litellm.RoleTool {
			toolMsg = &history[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("No tool message found in history")
	}
	if len(toolMsg.Blocks) != 1 {
		t.Fatalf("Tool blocks count = %d, want 1", len(toolMsg.Blocks))
	}
	result, ok := toolMsg.Blocks[0].(litellm.ToolResultBlock)
	if !ok {
		t.Fatalf("Tool block type = %T, want ToolResultBlock", toolMsg.Blocks[0])
	}
	if result.ToolUseID != "call-1" {
		t.Errorf("ToolUseID = %q, want %q", result.ToolUseID, "call-1")
	}
	if result.IsError {
		t.Error("IsError should be false")
	}
}

func TestSessionManager_AppendTool_IsError(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	blocks := []litellm.Block{litellm.TextBlock{Text: "command not found"}}
	m.AppendTool("sess-1", "call-2", blocks, true)

	m.AppendUser("sess-1", "hello")

	history := m.History("sess-1")
	for i := range history {
		if history[i].Role == litellm.RoleTool {
			result, ok := history[i].Blocks[0].(litellm.ToolResultBlock)
			if !ok {
				t.Fatal("Expected ToolResultBlock")
			}
			if !result.IsError {
				t.Error("IsError should be true")
			}
			return
		}
	}
	t.Fatal("No tool message found")
}

func TestSessionManager_HistoryPrependsSystemPrompt(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	m.AppendUser("sess-1", "hello")

	history := m.History("sess-1")
	if len(history) < 1 {
		t.Fatal("History is empty")
	}
	if history[0].Role != litellm.RoleSystem {
		t.Errorf("First message role = %q, want system", history[0].Role)
	}
	// System prompt should be a single text block
	if len(history[0].Blocks) != 1 {
		t.Fatalf("System blocks = %d, want 1", len(history[0].Blocks))
	}
	sysText, ok := history[0].Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("System block type = %T, want TextBlock", history[0].Blocks[0])
	}
	if sysText.Text == "" {
		t.Error("System prompt text should not be empty")
	}
}

func TestSessionManager_EmptySessionReturnsPromptOnly(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	history := m.History("sess-1")
	if len(history) != 1 {
		t.Fatalf("History length for empty session = %d, want 1 (system prompt only)", len(history))
	}
	if history[0].Role != litellm.RoleSystem {
		t.Errorf("Role = %q, want system", history[0].Role)
	}
}

func TestSessionManager_CloseRemovesSession(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")
	m.Close("sess-1")

	if m.Get("sess-1") {
		t.Error("Get returned true after Close")
	}

	// History of closed session should be empty
	history := m.History("sess-1")
	if len(history) != 0 {
		t.Errorf("History length after close = %d, want 0", len(history))
	}
}

func TestSessionManager_CloseUnknownIsNoop(t *testing.T) {
	m := NewSessionManager(50)
	m.Close("nonexistent") // should not panic
}

func TestSessionManager_AppendUserUnknownSession(t *testing.T) {
	m := NewSessionManager(50)
	m.AppendUser("nonexistent", "hello") // should not panic
}

func TestSessionManager_AppendAssistantUnknownSession(t *testing.T) {
	m := NewSessionManager(50)
	m.AppendAssistant("nonexistent", nil, nil) // should not panic
}

func TestSessionManager_AppendToolUnknownSession(t *testing.T) {
	m := NewSessionManager(50)
	m.AppendTool("nonexistent", "call-1", nil, false) // should not panic
}

func TestSessionManager_CreateTwiceIsNoop(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")
	m.Create("sess-1") // second create should not reset

	m.AppendUser("sess-1", "first message")
	history1 := m.History("sess-1")
	userCount1 := countUserMessages(history1)

	m.AppendUser("sess-1", "second message")
	history2 := m.History("sess-1")
	userCount2 := countUserMessages(history2)

	if userCount2 != userCount1+1 {
		t.Errorf("User messages before=%d, after=%d; want after=before+1", userCount1, userCount2)
	}
}

func TestSessionManager_HistoryDeepCopy(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")
	m.AppendUser("sess-1", "hello")

	history1 := m.History("sess-1")
	history2 := m.History("sess-1")

	// Modify history1 — should not affect history2
	if len(history1) > 0 {
		history1[0] = litellm.Message{} // zero out
	}
	if len(history2) > 0 && history2[0].Role == "" {
		t.Error("History() returned shared reference, not a copy")
	}
}

func TestSessionManager_DefaultExchangeLimit(t *testing.T) {
	m := NewSessionManager(0) // use default
	m.Create("sess-1")

	// Add more than the default 50 exchanges
	for i := 0; i < 60; i++ {
		m.AppendUser("sess-1", "message")
		m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "response"}}, nil)
	}

	history := m.History("sess-1")
	userCount := countUserMessages(history)
	if userCount > 50 {
		t.Errorf("User messages after 60 appends = %d, want <= 50", userCount)
	}
}

func TestSessionManager_WindowCapTrimsOldestFirst(t *testing.T) {
	m := NewSessionManager(3) // small window for testing
	m.Create("sess-1")

	m.AppendUser("sess-1", "first")
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp1"}}, nil)
	m.AppendUser("sess-1", "second")
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp2"}}, nil)
	m.AppendUser("sess-1", "third")
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp3"}}, nil)
	m.AppendUser("sess-1", "fourth")
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp4"}}, nil)

	history := m.History("sess-1")
	// Should have system + 3 most recent exchanges (3 user + 3 assistant = 6) = 7 messages
	if len(history) < 2 {
		t.Fatalf("History too short: %d", len(history))
	}

	// Check that "first" is gone
	for _, msg := range history {
		for _, block := range msg.Blocks {
			if text, ok := block.(litellm.TextBlock); ok && text.Text == "first" {
				t.Error("Found trimmed message 'first' in history")
			}
		}
	}

	// "fourth" and "resp4" should still be present
	foundFourth := false
	foundResp4 := false
	for _, msg := range history {
		for _, block := range msg.Blocks {
			if text, ok := block.(litellm.TextBlock); ok {
				if text.Text == "fourth" {
					foundFourth = true
				}
				if text.Text == "resp4" {
					foundResp4 = true
				}
			}
		}
	}
	if !foundFourth {
		t.Error("Most recent user message 'fourth' missing from history")
	}
	if !foundResp4 {
		t.Error("Most recent assistant response 'resp4' missing from history")
	}
}

func TestSessionManager_WindowCapWithToolMessages(t *testing.T) {
	m := NewSessionManager(2) // 2 exchanges
	m.Create("sess-1")

	// Exchange 1: user -> assistant (with tool call) -> tool result -> assistant (final)
	m.AppendUser("sess-1", "first")
	m.AppendAssistant("sess-1", nil, []litellm.ToolUseBlock{
		{ID: "call-1", Name: "file_viewer", Arguments: []byte(`{}`)},
	})
	m.AppendTool("sess-1", "call-1", []litellm.Block{litellm.TextBlock{Text: "content"}}, false)
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp1"}}, nil)

	// Exchange 2: user -> assistant (with tool call) -> tool result
	m.AppendUser("sess-1", "second")
	m.AppendAssistant("sess-1", nil, []litellm.ToolUseBlock{
		{ID: "call-2", Name: "terminal_execute", Arguments: []byte(`{}`)},
	})
	m.AppendTool("sess-1", "call-2", []litellm.Block{litellm.TextBlock{Text: "output"}}, false)

	// Exchange 3: user -> assistant (triggers trim)
	m.AppendUser("sess-1", "third")
	m.AppendAssistant("sess-1", []litellm.Block{litellm.TextBlock{Text: "resp3"}}, nil)

	history := m.History("sess-1")
	userCount := countUserMessages(history)

	if userCount > 2 {
		t.Errorf("User messages = %d, want <= 2 (maxExchanges=2)", userCount)
	}

	// "first" should be gone
	for _, msg := range history {
		for _, block := range msg.Blocks {
			if text, ok := block.(litellm.TextBlock); ok && text.Text == "first" {
				t.Error("Found trimmed user message 'first' in history")
			}
		}
	}
}

func TestSessionManager_AppendUserMultiblock(t *testing.T) {
	m := NewSessionManager(50)
	m.Create("sess-1")

	m.AppendUser("sess-1", "multi\nline\nmessage")

	history := m.History("sess-1")
	var userMsg *litellm.Message
	for i := range history {
		if history[i].Role == litellm.RoleUser {
			userMsg = &history[i]
			break
		}
	}
	if userMsg == nil {
		t.Fatal("No user message found")
	}
	if len(userMsg.Blocks) != 1 {
		t.Fatalf("User blocks = %d, want 1", len(userMsg.Blocks))
	}
	text, ok := userMsg.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("Block type = %T, want TextBlock", userMsg.Blocks[0])
	}
	if text.Text != "multi\nline\nmessage" {
		t.Errorf("Text = %q, want %q", text.Text, "multi\nline\nmessage")
	}
}

// helpers

func countUserMessages(history []litellm.Message) int {
	count := 0
	for _, m := range history {
		if m.Role == litellm.RoleUser {
			count++
		}
	}
	return count
}
