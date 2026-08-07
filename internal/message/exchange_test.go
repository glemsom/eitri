package message

import "testing"

// ── TrimExchanges ─────────────────────────────────────────────────────────

func TestTrimExchanges_BelowCapUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "resp1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "resp2"},
	}

	got := TrimExchanges(msgs, 5)
	if len(got) != len(msgs) {
		t.Fatalf("TrimExchanges below cap changed history: %d -> %d messages", len(msgs), len(got))
	}
	for i := range msgs {
		if got[i].Role != msgs[i].Role || got[i].Content != msgs[i].Content {
			t.Errorf("message %d changed below cap: %+v -> %+v", i, msgs[i], got[i])
		}
	}
}

func TestTrimExchanges_TrimsOldestExchangesFirst(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "resp1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "resp2"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "resp3"},
	}

	// Cap 2 exchanges: the oldest user message ("first") and everything
	// before it must go, bringing the user count down to the cap. This is the
	// exact sliding-window semantics of the history store's trim: the
	// assistant/tool tail that follows a trimmed user message survives.
	got := TrimExchanges(msgs, 2)
	if userCount(got) != 2 {
		t.Fatalf("user messages after trim = %d, want 2", userCount(got))
	}
	for _, m := range got {
		if m.Content == "first" {
			t.Fatal("trimmed user message 'first' still present")
		}
	}
	// The two most recent exchanges survive intact.
	if got[len(got)-1].Content != "resp3" || got[len(got)-2].Content != "third" {
		t.Errorf("most recent exchange lost: tail = %+v", got[len(got)-2:])
	}
}

func TestTrimExchanges_WithToolMessages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call-1", Type: "function", Function: FunctionCall{Name: "file_viewer", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "file contents"},
		{Role: "assistant", Content: "resp1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "resp2"},
		{Role: "user", Content: "third"},
		{Role: "assistant", Content: "resp3"},
	}

	got := TrimExchanges(msgs, 2)
	if userCount(got) != 2 {
		t.Fatalf("user messages after trim = %d, want 2", userCount(got))
	}
	for _, m := range got {
		if m.Content == "first" {
			t.Error("trimmed message 'first' still present")
		}
	}
	// The window keeps the tool messages that follow the surviving user
	// messages; the trimmed exchange's tail (its tool result) survives —
	// identical to the history store's sliding-window behaviour.
	foundTool := false
	for _, m := range got {
		if m.Role == "tool" && m.ToolCallID == "call-1" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Error("tool result of the trimmed exchange's tail was dropped")
	}
}

func TestTrimExchanges_NonPositiveCapUnchanged(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "first"}, {Role: "user", Content: "second"}}
	got := TrimExchanges(msgs, 0)
	if len(got) != len(msgs) {
		t.Fatalf("TrimExchanges(0) changed history: %d -> %d", len(msgs), len(got))
	}
	got = TrimExchanges(msgs, -3)
	if len(got) != len(msgs) {
		t.Fatalf("TrimExchanges(-3) changed history: %d -> %d", len(msgs), len(got))
	}
}

func TestTrimExchanges_AtCapUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "resp1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "resp2"},
	}
	got := TrimExchanges(msgs, 2)
	if len(got) != len(msgs) {
		t.Fatalf("TrimExchanges at cap changed history: %d -> %d", len(msgs), len(got))
	}
}

// ── RepairPendingToolUse ──────────────────────────────────────────────────

func TestRepairPendingToolUse_ClosesTrailingAssistantToolCall(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_123", Type: "function", Function: FunctionCall{Name: "browser", Arguments: `{"action":"navigate"}`}}}},
	}

	fixed := RepairPendingToolUse(msgs)
	if len(fixed) != len(msgs)+1 {
		t.Fatalf("RepairPendingToolUse length = %d, want %d (synthetic error appended)", len(fixed), len(msgs)+1)
	}
	last := fixed[len(fixed)-1]
	if last.Role != "tool" {
		t.Fatalf("last message role = %q, want \"tool\"", last.Role)
	}
	if last.ToolCallID != "call_123" {
		t.Errorf("last tool_call_id = %q, want \"call_123\"", last.ToolCallID)
	}
	if last.Content == "" {
		t.Error("synthetic tool error has empty content")
	}
	if last.CreatedAt.IsZero() {
		t.Error("synthetic tool error has zero CreatedAt")
	}
}

func TestRepairPendingToolUse_NoToolCallUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	fixed := RepairPendingToolUse(msgs)
	if len(fixed) != len(msgs) {
		t.Fatalf("RepairPendingToolUse changed history without a pending tool call: %d -> %d", len(msgs), len(fixed))
	}
}

func TestRepairPendingToolUse_ResolvedToolCallUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: FunctionCall{Name: "bash", Arguments: `{"cmd":"echo hi"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "hi"},
	}
	fixed := RepairPendingToolUse(msgs)
	if len(fixed) != len(msgs) {
		t.Fatalf("RepairPendingToolUse changed a resolved tool call: %d -> %d", len(msgs), len(fixed))
	}
}

func TestRepairPendingToolUse_EmptyNoop(t *testing.T) {
	if fixed := RepairPendingToolUse(nil); len(fixed) != 0 {
		t.Fatalf("RepairPendingToolUse(nil) = %d messages, want 0", len(fixed))
	}
}

func TestRepairPendingToolUse_NonAssistantTailUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}
	fixed := RepairPendingToolUse(msgs)
	if len(fixed) != len(msgs) {
		t.Fatalf("RepairPendingToolUse changed history ending in a tool message: %d -> %d", len(msgs), len(fixed))
	}
}

func userCount(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "user" {
			n++
		}
	}
	return n
}
