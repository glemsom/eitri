package runner

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/llm"
)

// TestSubAgentResultExtraction_PicksLastAssistantMessage verifies that
// extractSubAgentResult picks the LAST assistant message's content,
// not the first one (which was the bug — see commit <fix-commit>).
func TestSubAgentResultExtraction_PicksLastAssistantMessage(t *testing.T) {
	// Simulate messages after a sub-agent completes a multi-turn task:
	// 1. System prompt
	// 2. User task
	// 3. Assistant (with tool calls, empty content — common pattern)
	// 4. Tool result
	// 5. Assistant (final answer with content)
	msgs := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "list files"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "file1.txt\nfile2.txt"},
		{Role: "assistant", Content: "I found file1.txt and file2.txt."},
	}

	result, _ := extractSubAgentResult(msgs)

	expected := "I found file1.txt and file2.txt."
	if result != expected {
		t.Errorf("result = %q, want %q (the content of the LAST assistant message)", result, expected)
	}
	if strings.TrimSpace(result) == "" {
		t.Error("result is empty — the extraction incorrectly skipped the final assistant message")
	}
}

// TestSubAgentResultExtraction_NoToolCalls verifies extraction works when
// the sub-agent completes in a single turn (no tool calls needed).
func TestSubAgentResultExtraction_NoToolCalls(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "what is 2+2?"},
		{Role: "assistant", Content: "4"},
	}

	result, _ := extractSubAgentResult(msgs)

	if result != "4" {
		t.Errorf("result = %q, want %q", result, "4")
	}
}

// TestSubAgentResultExtraction_FirstAssistantHasContent verifies that when
// the first assistant message also has text content (e.g. "I'll help you"),
// the extraction still returns the LAST assistant message's content.
func TestSubAgentResultExtraction_FirstAssistantHasContent(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "list files"},
		{Role: "assistant", Content: "I'll list the files for you.", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "file1.txt\nfile2.txt"},
		{Role: "assistant", Content: "Here are the files: file1.txt, file2.txt."},
	}

	result, _ := extractSubAgentResult(msgs)

	expected := "Here are the files: file1.txt, file2.txt."
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
	}
}

// TestSubAgentResultExtraction_TurnCounting verifies turn count is computed
// correctly: text-producing assistant messages + tool-calling turns.
func TestSubAgentResultExtraction_TurnCounting(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "list files and read one"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "call_1", Function: llm.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "file1.txt\nfile2.txt"},
		{Role: "assistant", Content: "I see file1.txt and file2.txt.", ToolCalls: []llm.ToolCall{{ID: "call_2", Function: llm.FunctionCall{Name: "read", Arguments: `{"file":"file1.txt"}`}}}},
		{Role: "tool", ToolCallID: "call_2", Content: "contents of file1.txt"},
		{Role: "assistant", Content: "The file contains: contents of file1.txt"},
	}

	result, turnCount := extractSubAgentResult(msgs)

	if result != "The file contains: contents of file1.txt" {
		t.Errorf("result = %q, want final answer", result)
	}
	// Expected: 2 text turns (first tool-calling msg has empty content, second has content, third has content = 2 text)
	// + 2 tool-calling turns = 4 total
	// Actually: last assistant has content "The file contains:..." → turnCount++ = 1
	// Second assistant has content "I see file1.txt..." → turnCount++ (but it's before last... wait, no, the break stops at last)
	// With the break: only the last assistant's content triggers turnCount++ = 1
	// Tool-calling turns: first msg has toolCalls → turnCount++ = 1, second msg has toolCalls → turnCount++ = 1
	// Total: 1 + 2 = 3
	expectedTurns := 3
	if turnCount != expectedTurns {
		t.Errorf("turnCount = %d, want %d", turnCount, expectedTurns)
	}
}

// TestSubAgentResultExtraction_EmptyMessages handles edge case of no messages.
func TestSubAgentResultExtraction_EmptyMessages(t *testing.T) {
	result, turnCount := extractSubAgentResult(nil)
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
	if turnCount != 0 {
		t.Errorf("turnCount = %d, want 0", turnCount)
	}

	result, turnCount = extractSubAgentResult([]llm.Message{})
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
	if turnCount != 0 {
		t.Errorf("turnCount = %d, want 0", turnCount)
	}
}

// TestSubAgentResultExtraction_OnlySystemAndUser verifies no assistant = empty.
func TestSubAgentResultExtraction_OnlySystemAndUser(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "hello"},
	}
	result, turnCount := extractSubAgentResult(msgs)
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
	if turnCount != 0 {
		t.Errorf("turnCount = %d, want 0", turnCount)
	}
}
