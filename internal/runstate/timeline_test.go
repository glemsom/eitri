package runstate

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCondensedEvents_FiltersTokenAndThinkingDelta(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.Token("hello")
	w.ToolCall("grep", json.RawMessage(`{"pattern":"test"}`))
	w.ThinkingDelta("reasoning...")
	w.ToolResult("grep", "found 3 matches")
	w.Done("msg_1", nil)

	events := s.CondensedEvents()

	// Should exclude token and thinking_delta
	for _, evt := range events {
		if evt.Type == "token" || evt.Type == "thinking_delta" {
			t.Errorf("condensed event should not contain %q type", evt.Type)
		}
	}

	// Should include semantic events
	if len(events) != 3 {
		t.Errorf("expected 3 condensed events (tool_call, tool_result, done), got %d", len(events))
		for i, evt := range events {
			t.Logf("  events[%d] = %+v", i, evt)
		}
	}

	if len(events) >= 1 && events[0].Type != "tool_call" {
		t.Errorf("expected first event type 'tool_call', got %q", events[0].Type)
	}
	if len(events) >= 1 && events[0].Tool != "grep" {
		t.Errorf("expected first event tool 'grep', got %q", events[0].Tool)
	}
	if len(events) >= 2 && events[1].Type != "tool_result" {
		t.Errorf("expected second event type 'tool_result', got %q", events[1].Type)
	}
	if len(events) >= 2 && events[1].Output != "found 3 matches" {
		t.Errorf("expected second event output 'found 3 matches', got %q", events[1].Output)
	}
	if len(events) >= 3 && events[2].Type != "done" {
		t.Errorf("expected third event type 'done', got %q", events[2].Type)
	}
}

func TestCondensedEvents_IncludesTurnNumber(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.SetTurn(1)
	w.ToolCall("grep", nil)

	w.SetTurn(2)
	w.ToolCall("read", nil)

	events := s.CondensedEvents()

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Turn != 1 {
		t.Errorf("expected first event turn 1, got %d", events[0].Turn)
	}
	if events[1].Turn != 2 {
		t.Errorf("expected second event turn 2, got %d", events[1].Turn)
	}
}

func TestCondensedEvents_ContextUpdate(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.ContextUpdate(&ContextUpdate{
		TotalTokens:   5000,
		PromptTokens:  4000,
		ContextWindow: 128000,
	})

	events := s.CondensedEvents()

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "context_update" {
		t.Errorf("expected type 'context_update', got %q", events[0].Type)
	}
	if events[0].TotalTokens != 5000 {
		t.Errorf("expected TotalTokens 5000, got %d", events[0].TotalTokens)
	}
	if events[0].PromptTokens != 4000 {
		t.Errorf("expected PromptTokens 4000, got %d", events[0].PromptTokens)
	}
	if events[0].ContextWindow != 128000 {
		t.Errorf("expected ContextWindow 128000, got %d", events[0].ContextWindow)
	}
}

func TestCondensedEvents_EmptyState(t *testing.T) {
	s := New()
	events := s.CondensedEvents()

	if len(events) != 0 {
		t.Errorf("expected 0 events from empty state, got %d", len(events))
	}
}

func TestCondensedEvents_ToolResultErrorDetection(t *testing.T) {
	s := New()
	w := NewWriter(s)

	// Simulate a tool dispatch error
	w.ToolResult("read", "Tool error: unknown tool: \"read\"")

	events := s.CondensedEvents()

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].Error {
		t.Error("expected Error=true for tool dispatch error output")
	}
}

func TestCondensedEvents_ToolResultNoError(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.ToolResult("grep", "found 10 results")

	events := s.CondensedEvents()

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Error {
		t.Error("expected Error=false for successful tool result")
	}
}

func TestCondensedEvents_SkillActivated(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.SkillActivated("my-skill")

	events := s.CondensedEvents()

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "skill_activated" {
		t.Errorf("expected type 'skill_activated', got %q", events[0].Type)
	}
	if events[0].SkillName != "my-skill" {
		t.Errorf("expected SkillName 'my-skill', got %q", events[0].SkillName)
	}
}

func TestCondensedEvents_NeedsConfirmation(t *testing.T) {
	s := New()
	s.Broadcast(SSEEvent{
		Type:    "needs_confirmation",
		Content: "Allow access to /etc/passwd?",
		Data: map[string]any{
			"path":    "/etc/passwd",
			"message": "Allow access to /etc/passwd?",
		},
	})

	events := s.CondensedEvents()

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "needs_confirmation" {
		t.Errorf("expected type 'needs_confirmation', got %q", events[0].Type)
	}
	if events[0].ConfirmPath != "/etc/passwd" {
		t.Errorf("expected ConfirmPath '/etc/passwd', got %q", events[0].ConfirmPath)
	}
	if events[0].ConfirmMessage != "Allow access to /etc/passwd?" {
		t.Errorf("expected ConfirmMessage 'Allow access to /etc/passwd?', got %q", events[0].ConfirmMessage)
	}
}

func TestCondensedEvents_LLMCallCorrelation(t *testing.T) {
	s := New()
	w := NewWriter(s)

	w.SetTurn(2)
	w.LLMCall(LLMCallInfo{
		TraceID:    "trace_abc",
		Attempt:    2,
		Attempts:   3,
		DurationMs: 900,
		TTFBMs:     40,
		TTFTMs:     120,
	})
	w.ToolCall("bash", json.RawMessage(`{"cmd":"ls"}`))

	events := s.CondensedEvents()

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	llm := events[0]
	if llm.Type != "llm_call" {
		t.Errorf("expected type 'llm_call', got %q", llm.Type)
	}
	if llm.Turn != 2 {
		t.Errorf("expected Turn 2, got %d", llm.Turn)
	}
	if llm.TraceID != "trace_abc" {
		t.Errorf("TraceID = %q, want trace_abc", llm.TraceID)
	}
	if llm.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2", llm.Attempt)
	}
	if llm.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", llm.Attempts)
	}
	if llm.DurationMs != 900 || llm.TTFBMs != 40 || llm.TTFTMs != 120 {
		t.Errorf("timing = duration:%d ttfb:%d ttft:%d, want 900/40/120", llm.DurationMs, llm.TTFBMs, llm.TTFTMs)
	}
}

func TestGenerateRunID_IsDeterministic(t *testing.T) {
	sessionID := "test-session"
	startedAt := time.Date(2026, 7, 25, 17, 46, 46, 0, time.UTC)

	id1 := GenerateRunID(sessionID, startedAt)
	id2 := GenerateRunID(sessionID, startedAt)

	if id1 != id2 {
		t.Errorf("expected same run_id for same inputs, got %q vs %q", id1, id2)
	}
}

func TestGenerateRunID_DifferentInputs(t *testing.T) {
	id1 := GenerateRunID("session-a", time.Now())
	id2 := GenerateRunID("session-b", time.Now())

	if id1 == id2 {
		t.Error("expected different run_ids for different sessions")
	}
}

func TestGenerateRunID_Format(t *testing.T) {
	id := GenerateRunID("sid", time.Now())

	if len(id) != 16 {
		t.Errorf("expected 16-char hex run_id, got %q (len=%d)", id, len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("run_id contains non-hex character %q", c)
		}
	}
}

func TestTimelineJSON_Marshal(t *testing.T) {
	now := time.Date(2026, 7, 25, 17, 46, 46, 0, time.UTC)
	timeline := Timeline{
		Version:   1,
		RunID:     "abc123def456",
		SessionID: "session-1",
		Provider: TimelineProvider{
			Model:      "gpt-4",
			ProviderID: "opencode_go",
		},
		StartedAt: now,
		EndedAt:   now.Add(time.Minute),
		Termination: &TimelineTermination{
			Reason:  TerminationCompleted,
			Message: "",
		},
		Events: []TimelineEvent{
			{
				Type:      "tool_call",
				Timestamp: now,
				Turn:      1,
				Tool:      "grep",
				Args:      json.RawMessage(`{"pattern":"test"}`),
			},
		},
	}

	data, err := json.Marshal(timeline)
	if err != nil {
		t.Fatalf("failed to marshal timeline: %v", err)
	}

	var decoded Timeline
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal timeline: %v", err)
	}

	if decoded.Version != 1 {
		t.Errorf("expected Version 1, got %d", decoded.Version)
	}
	if decoded.RunID != "abc123def456" {
		t.Errorf("expected RunID 'abc123def456', got %q", decoded.RunID)
	}
	if decoded.Provider.Model != "gpt-4" {
		t.Errorf("expected Model 'gpt-4', got %q", decoded.Provider.Model)
	}
	if decoded.Termination.Reason != TerminationCompleted {
		t.Errorf("expected Termination reason 'completed', got %q", decoded.Termination.Reason)
	}
	if len(decoded.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(decoded.Events))
	}
	if decoded.Events[0].Tool != "grep" {
		t.Errorf("expected event tool 'grep', got %q", decoded.Events[0].Tool)
	}
}
