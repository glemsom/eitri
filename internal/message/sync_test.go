package message

import (
	"testing"
	"time"

	"github.com/voocel/litellm"
)

// TestSyncHistoryToConversation_StripsLeadingSystemMessage verifies the
// strip-system-message invariant (ADR-0028): the system prompt is stored
// separately (UISession.SystemPrompt / the history manager's system prompt),
// so it must never appear in the conversation message list.
func TestSyncHistoryToConversation_StripsLeadingSystemMessage(t *testing.T) {
	hist := []EitriMessage{
		{Message: litellm.Message{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are Eitri."}}}},
		{Message: litellm.Message{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}}},
		{Message: litellm.Message{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
	}

	got := SyncHistoryToConversation(hist)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (system stripped)", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Errorf("first message = %+v, want user 'hello'", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hi" {
		t.Errorf("second message = %+v, want assistant 'hi'", got[1])
	}
}

// TestSyncHistoryToConversation_ToolCallAndResultMapping verifies tool-use
// and tool-result messages map to the flat shape with their IDs preserved.
func TestSyncHistoryToConversation_ToolCallAndResultMapping(t *testing.T) {
	hist := []EitriMessage{
		{Message: litellm.Message{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}}},
		{Message: litellm.Message{Role: litellm.Role("assistant"), Blocks: []litellm.Block{
			litellm.ToolUseBlock{ID: "call_1", Name: "bash", Arguments: []byte(`{"cmd":"ls"}`)},
		}}},
		{Message: litellm.Message{Role: litellm.Role("tool"), Blocks: []litellm.Block{
			litellm.ToolResultBlock{ToolUseID: "call_1", Content: []litellm.Block{litellm.TextBlock{Text: "out"}}},
		}}},
	}

	got := SyncHistoryToConversation(hist)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if len(got[0].ToolCalls) != 1 {
		t.Fatalf("assistant ToolCalls = %d, want 1", len(got[0].ToolCalls))
	}
	if got[0].ToolCalls[0].ID != "call_1" || got[0].ToolCalls[0].Function.Name != "bash" {
		t.Errorf("tool call = %+v, want call_1/bash", got[0].ToolCalls[0])
	}
	if got[0].ToolCalls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool call arguments = %q, want {\"cmd\":\"ls\"}", got[0].ToolCalls[0].Function.Arguments)
	}
	if got[1].Role != "tool" || got[1].ToolCallID != "call_1" || got[1].Content != "out" {
		t.Errorf("tool result = %+v, want tool call_1 'out'", got[1])
	}
}

// TestSyncHistoryToConversation_EmptyHistory verifies an empty or nil history
// converts to an empty (non-nil for nil input) message list.
func TestSyncHistoryToConversation_EmptyHistory(t *testing.T) {
	if got := SyncHistoryToConversation(nil); got != nil && len(got) != 0 {
		t.Fatalf("nil history → %d messages, want empty", len(got))
	}
	if got := SyncHistoryToConversation([]EitriMessage{}); len(got) != 0 {
		t.Fatalf("empty history → %d messages, want 0", len(got))
	}
}

// TestSyncHistoryToConversation_PreservesUIMetadata verifies UI-only fields
// (CreatedAt, Components, QuickReplies) survive the conversion.
func TestSyncHistoryToConversation_PreservesUIMetadata(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hist := []EitriMessage{
		{Message: litellm.Message{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}}},
		{
			Message:      litellm.Message{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
			CreatedAt:    created,
			Components:   []ComponentData{{Name: "card", Data: map[string]any{"k": "v"}}},
			QuickReplies: []string{"yes", "no"},
		},
	}

	got := SyncHistoryToConversation(hist)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if !got[0].CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got[0].CreatedAt, created)
	}
	if len(got[0].Components) != 1 || got[0].Components[0].Name != "card" {
		t.Errorf("Components = %+v, want card", got[0].Components)
	}
	if len(got[0].QuickReplies) != 2 {
		t.Errorf("QuickReplies = %v, want [yes no]", got[0].QuickReplies)
	}
}

// TestStripLeadingSystemMessage_Passthrough verifies the strip helper is a
// no-op on lists that do not start with a system message (e.g. compacted
// message lists already stripped by the compactor) and safe on empty input.
func TestStripLeadingSystemMessage_Passthrough(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	got := StripLeadingSystemMessage(msgs)
	if len(got) != 2 || got[0].Content != "a" {
		t.Fatalf("passthrough changed the list: %+v", got)
	}

	// Nil and empty inputs are safe.
	if got := StripLeadingSystemMessage(nil); got != nil {
		t.Fatalf("nil input → %+v, want nil", got)
	}
	if got := StripLeadingSystemMessage([]Message{}); len(got) != 0 {
		t.Fatalf("empty input → %d messages, want 0", len(got))
	}
}
