package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
)

// newTestService creates a report Service backed by a temp directory.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "report-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}
	return New(p), dir
}

// writeTestTimeline writes a timeline file for testing.
func writeTestTimeline(t *testing.T, dir, sessionID string, tl *timeline.Timeline) {
	t.Helper()
	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}
	if err := p.SaveTimeline(sessionID, tl); err != nil {
		t.Fatalf("failed to save timeline: %v", err)
	}
}

func TestListRuns_NoData(t *testing.T) {
	svc, _ := newTestService(t)
	runs, err := svc.ListRuns("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runs != nil && len(runs) > 0 {
		t.Errorf("expected empty runs, got %d", len(runs))
	}
}

func TestListRuns_SingleTimeline(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-1"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "OK", Error: false},
			{Type: "tool_call", Timestamp: now, Turn: 2, Tool: "read"},
			{Type: "tool_result", Timestamp: now, Turn: 2, Tool: "read", Output: "data", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	runs, err := svc.ListRuns(sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Run != 0 {
		t.Errorf("expected run index 0, got %d", runs[0].Run)
	}
	if runs[0].Turns != 2 {
		t.Errorf("expected 2 turns, got %d", runs[0].Turns)
	}
	if runs[0].Termination.Reason != timeline.TerminationCompleted {
		t.Errorf("expected completed, got %s", runs[0].Termination.Reason)
	}
}

func TestListRuns_MultipleTimelines(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-multi"
	now := time.Now().UTC()

	// Write two timelines with different start times
	tl1 := &timeline.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
		},
	}
	tl2 := &timeline.Timeline{
		Version:   1,
		RunID:     "run-2",
		SessionID: sessionID,
		StartedAt: now.Add(30 * time.Second),
		EndedAt:   now.Add(45 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCancelled,
			Message: "user cancelled",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now.Add(30 * time.Second), Turn: 1, Tool: "read"},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl1)
	writeTestTimeline(t, dir, sessionID, tl2)

	runs, err := svc.ListRuns(sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].Termination.Reason != timeline.TerminationCompleted {
		t.Errorf("expected first run completed, got %s", runs[0].Termination.Reason)
	}
	if runs[1].Termination.Reason != timeline.TerminationCancelled {
		t.Errorf("expected second run cancelled, got %s", runs[1].Termination.Reason)
	}
}

func TestGetReport_FullTimeline(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-full"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-full",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: now,
		EndedAt:   now.Add(30 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationError,
			Message: "something went wrong",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "file.txt", Error: false},
			{Type: "context_update", Timestamp: now, Turn: 1, TotalTokens: 500, PromptTokens: 400, ContextWindow: 128000},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep == nil {
		t.Fatal("expected non-nil report")
	}

	if rep.ReportVersion != "full" {
		t.Errorf("expected report_version 'full', got %q", rep.ReportVersion)
	}
	if rep.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", rep.Model)
	}
	if rep.Provider != "test-provider" {
		t.Errorf("expected provider 'test-provider', got %q", rep.Provider)
	}
	if rep.Termination == nil || rep.Termination.Reason != timeline.TerminationError {
		t.Errorf("expected termination reason 'error', got %v", rep.Termination)
	}
	if rep.DurationMs != 30000 {
		t.Errorf("expected duration 30000ms, got %d", rep.DurationMs)
	}

	// Should have 2 turns (user + assistant) since we interleave
	if len(rep.Turns) != 2 {
		t.Fatalf("expected 2 turns (user + assistant), got %d", len(rep.Turns))
	}
	if rep.Turns[0].Role != "user" {
		t.Errorf("expected first turn role 'user', got %q", rep.Turns[0].Role)
	}
	if rep.Turns[1].Role != "assistant" {
		t.Errorf("expected second turn role 'assistant', got %q", rep.Turns[1].Role)
	}
	if len(rep.Turns[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(rep.Turns[1].ToolCalls))
	}
	if rep.Turns[1].ToolCalls[0].Name != "bash" {
		t.Errorf("expected tool 'bash', got %q", rep.Turns[1].ToolCalls[0].Name)
	}
	if rep.Turns[1].ContextBefore != nil {
		t.Error("expected context_before to be nil for first turn")
	}
	if rep.Turns[1].ContextAfter == nil || rep.Turns[1].ContextAfter.TotalTokens != 500 {
		t.Error("expected context_after with TotalTokens=500")
	}

	// Summary
	if rep.Summary.TotalTurns != 1 {
		t.Errorf("expected 1 assistant turn, got %d", rep.Summary.TotalTurns)
	}
	if rep.Summary.TotalToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", rep.Summary.TotalToolCalls)
	}
}

func TestGetReport_Reconstructed(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-recon"
	now := time.Now().UTC()

	// Write session snapshot
	sessionDir := filepath.Join(dir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	sessionData, _ := json.Marshal(map[string]any{
		"id":        sessionID,
		"title":     "Reconstructed Session",
		"workspace": "/tmp/test",
		"messages": []map[string]any{
			{"role": "user", "content": "Hello", "created_at": now},
			{"role": "assistant", "content": "Hi there!", "created_at": now},
		},
	})
	snapshotFile := filepath.Join(sessionDir, "2025-01-01T00-00-00.json")
	if err := os.WriteFile(snapshotFile, sessionData, 0600); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	symlink := filepath.Join(sessionDir, "session.json")
	os.Remove(symlink)
	if err := os.Symlink("2025-01-01T00-00-00.json", symlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Write history
	histData, _ := json.Marshal(persist.HistorySchema{
		Version: 1,
		Messages: []message.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
	})
	historyDir := filepath.Join(dir, "history", sessionID)
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatalf("failed to create history dir: %v", err)
	}
	histFile := filepath.Join(historyDir, "2025-01-01T00-00-00.json")
	if err := os.WriteFile(histFile, histData, 0600); err != nil {
		t.Fatalf("failed to write history: %v", err)
	}
	histSymlink := filepath.Join(historyDir, "history.json")
	os.Remove(histSymlink)
	if err := os.Symlink("2025-01-01T00-00-00.json", histSymlink); err != nil {
		t.Fatalf("failed to create history symlink: %v", err)
	}

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep == nil {
		t.Fatal("expected non-nil report")
	}

	if rep.ReportVersion != "reconstructed" {
		t.Errorf("expected report_version 'reconstructed', got %q", rep.ReportVersion)
	}
	if rep.Title != "Reconstructed Session" {
		t.Errorf("expected title 'Reconstructed Session', got %q", rep.Title)
	}
	if rep.SystemPrompt != "" {
		t.Errorf("expected empty SystemPrompt (not in snapshot), got %q", rep.SystemPrompt)
	}
	if rep.Summary.Note != "limited data — no timeline persisted for this session" {
		t.Errorf("expected note about limited data, got %q", rep.Summary.Note)
	}
	if len(rep.Turns) == 0 {
		t.Fatal("expected at least 1 turn in reconstructed report")
	}
}

func TestGetReport_Reconstructed_WithSystemPrompt(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-recon-sp"
	now := time.Now().UTC()

	// Write session snapshot WITH system_prompt
	sessionDir := filepath.Join(dir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	sessionData, _ := json.Marshal(map[string]any{
		"id":            sessionID,
		"title":         "Reconstructed With SP",
		"workspace":     "/tmp/test",
		"system_prompt": "You are Eitri, an expert AI coding agent.",
		"messages": []map[string]any{
			{"role": "user", "content": "Hello", "created_at": now},
			{"role": "assistant", "content": "Hi there!", "created_at": now},
		},
	})
	snapshotFile := filepath.Join(sessionDir, "2025-01-01T00-00-00.json")
	if err := os.WriteFile(snapshotFile, sessionData, 0600); err != nil {
		t.Fatalf("failed to write snapshot: %v", err)
	}
	symlink := filepath.Join(sessionDir, "session.json")
	os.Remove(symlink)
	if err := os.Symlink("2025-01-01T00-00-00.json", symlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Write history
	histData, _ := json.Marshal(persist.HistorySchema{
		Version: 1,
		Messages: []message.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
	})
	historyDir := filepath.Join(dir, "history", sessionID)
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatalf("failed to create history dir: %v", err)
	}
	histFile := filepath.Join(historyDir, "2025-01-01T00-00-00.json")
	if err := os.WriteFile(histFile, histData, 0600); err != nil {
		t.Fatalf("failed to write history: %v", err)
	}
	histSymlink := filepath.Join(historyDir, "history.json")
	os.Remove(histSymlink)
	if err := os.Symlink("2025-01-01T00-00-00.json", histSymlink); err != nil {
		t.Fatalf("failed to create history symlink: %v", err)
	}

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep == nil {
		t.Fatal("expected non-nil report")
	}

	if rep.ReportVersion != "reconstructed" {
		t.Errorf("expected report_version 'reconstructed', got %q", rep.ReportVersion)
	}
	if rep.SystemPrompt != "You are Eitri, an expert AI coding agent." {
		t.Errorf("expected SystemPrompt to be carried from snapshot, got %q", rep.SystemPrompt)
	}
}

func TestGetReport_RunIndexOutOfRange(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-range"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Events:    []timeline.TimelineEvent{},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	_, err := svc.GetReport(sessionID, 5)
	if err == nil {
		t.Error("expected error for out-of-range run index")
	}
}

func TestSummary_FailedTools(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-fail"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-fail",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(30 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "read", Output: "Tool error: file not found", Error: true},
			{Type: "tool_call", Timestamp: now, Turn: 2, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 2, Tool: "grep", Output: "results", Error: false},
			{Type: "tool_call", Timestamp: now, Turn: 2, Tool: "read"},
			{Type: "tool_result", Timestamp: now, Turn: 2, Tool: "read", Output: "Tool error: permission denied", Error: true},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Summary.FailedToolCalls != 2 {
		t.Errorf("expected 2 failed tool calls, got %d", rep.Summary.FailedToolCalls)
	}
	if len(rep.Summary.FailedToolNames) == 0 {
		t.Errorf("expected failed tool names, got none")
	}
}

func TestGetReport_TurnsFollowEmissionOrder(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-emission-order"
	now := time.Now().UTC()

	// Emission order differs from turn-number order: turn 2 is emitted before
	// turn 1 (e.g. after compaction or trimmed history). The report must follow
	// the timeline's emission order, not re-sort by turn number.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-emission-order",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 2, Tool: "read"},
			{Type: "tool_result", Timestamp: now, Turn: 2, Tool: "read", Output: "later", Error: false},
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "earlier", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect assistant turns in report order.
	assistants := make([]Turn, 0)
	for _, turn := range rep.Turns {
		if turn.Role == "assistant" {
			assistants = append(assistants, turn)
		}
	}
	if len(assistants) != 2 {
		t.Fatalf("expected 2 assistant turns, got %d", len(assistants))
	}

	// First emitted assistant turn (turn 2) must come first.
	if assistants[0].Turn != 2 {
		t.Errorf("expected first assistant turn to be turn 2 (emission order), got %d", assistants[0].Turn)
	}
	if assistants[1].Turn != 1 {
		t.Errorf("expected second assistant turn to be turn 1 (emission order), got %d", assistants[1].Turn)
	}

	// The emitted-first turn 2 kept its tool call (read) attached.
	if len(assistants[0].ToolCalls) != 1 || assistants[0].ToolCalls[0].Name != "read" {
		t.Errorf("expected emitted-first turn 2 to hold its 'read' tool call, got %d calls", len(assistants[0].ToolCalls))
	}
	if len(assistants[1].ToolCalls) != 1 || assistants[1].ToolCalls[0].Name != "grep" {
		t.Errorf("expected second turn 1 to hold its 'grep' tool call, got %d calls", len(assistants[1].ToolCalls))
	}
}

func TestGetReport_ToolCallsKeepEmissionOrder(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-tool-order"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-tool-order",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now, Turn: 1, DurationMs: 100},
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "a", Error: false},
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "b", Error: false},
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "read", Output: "c", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}
	if len(assistant.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(assistant.ToolCalls))
	}
	wantNames := []string{"bash", "grep", "read"}
	for i, tc := range assistant.ToolCalls {
		if tc.Name != wantNames[i] {
			t.Errorf("tool call %d name = %q, want %q (emission order)", i, tc.Name, wantNames[i])
		}
	}
}

// writeTestTrace writes an HTTP trace file for testing.
func writeTestTrace(t *testing.T, dir, sessionID string, trace *debug.HTTPTrace) {
	t.Helper()
	tracesDir := filepath.Join(dir, "sessions", sessionID, "traces")
	if err := os.MkdirAll(tracesDir, 0o700); err != nil {
		t.Fatalf("failed to create traces dir: %v", err)
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("failed to marshal trace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracesDir, string(trace.ID)+".json"), data, 0o600); err != nil {
		t.Fatalf("failed to write trace: %v", err)
	}
}

func TestGetReport_TurnsReflectEnrichedTraceFields(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-trace-enrich"
	now := time.Now().UTC()

	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-trace-enrich",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "file.txt", Error: false},
			{Type: "context_update", Timestamp: now, Turn: 1, TotalTokens: 500, PromptTokens: 400, ContextWindow: 128000},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// A trace recorded for the assistant turn, enriched with provider usage,
	// finish reason, model, attempt, and TTFB.
	trace := &debug.HTTPTrace{
		ID:            "trace_1",
		Timestamp:     now,
		SessionID:     sessionID,
		ProviderID:    "test-provider",
		Status:        200,
		DurationMs:    1200,
		RequestBytes:  100,
		ResponseBytes: 500,
		TTFBMs:        80,
		Attempt:       1,
		Model:         "test-model",
		FinishReason:  "stop",
		Usage: &debug.UsageTotals{
			PromptTokens:     400,
			CompletionTokens: 50,
			TotalTokens:      450,
			CacheReadTokens:  120,
			CacheWriteTokens: 30,
		},
	}
	writeTestTrace(t, dir, sessionID, trace)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep == nil {
		t.Fatal("expected non-nil report")
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}

	if assistant.LLMTraceID != "trace_1" {
		t.Errorf("LLMTraceID = %q, want %q", assistant.LLMTraceID, "trace_1")
	}
	if assistant.LLMDurationMs != 1200 {
		t.Errorf("LLMDurationMs = %d, want 1200", assistant.LLMDurationMs)
	}
	if assistant.LLMRequestBytes != 100 || assistant.LLMResponseBytes != 500 {
		t.Errorf("LLM bytes = req:%d resp:%d, want 100/500", assistant.LLMRequestBytes, assistant.LLMResponseBytes)
	}
	if assistant.LLMTTFBMs != 80 {
		t.Errorf("LLMTTFBMs = %d, want 80", assistant.LLMTTFBMs)
	}
	if assistant.LLMAttempt != 1 {
		t.Errorf("LLMAttempt = %d, want 1", assistant.LLMAttempt)
	}
	if assistant.LLMModel != "test-model" {
		t.Errorf("LLMModel = %q, want %q", assistant.LLMModel, "test-model")
	}
	if assistant.LLMFinishReason != "stop" {
		t.Errorf("LLMFinishReason = %q, want %q", assistant.LLMFinishReason, "stop")
	}
	if assistant.LLMUsage == nil {
		t.Fatal("expected LLMUsage on turn")
	} else {
		if assistant.LLMUsage.PromptTokens != 400 || assistant.LLMUsage.CompletionTokens != 50 || assistant.LLMUsage.TotalTokens != 450 {
			t.Errorf("LLMUsage = %+v, want prompt=400 completion=50 total=450", assistant.LLMUsage)
		}
		if assistant.LLMUsage.CacheReadTokens != 120 || assistant.LLMUsage.CacheWriteTokens != 30 {
			t.Errorf("LLMUsage cache = read:%d write:%d, want 120/30", assistant.LLMUsage.CacheReadTokens, assistant.LLMUsage.CacheWriteTokens)
		}
	}

	if rep.Summary.TotalCacheReadTokens != 120 || rep.Summary.TotalCacheWriteTokens != 30 {
		t.Errorf("summary cache = read:%d write:%d, want 120/30", rep.Summary.TotalCacheReadTokens, rep.Summary.TotalCacheWriteTokens)
	}
}

func TestGetReport_JoinsTurnToTraceByID(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-join-id"
	now := time.Now().UTC()

	// The turn's LLM call happened 2 minutes before the trace was finalized —
	// far outside the old ±30s window. Only the trace_id recorded on the
	// timeline (at write time) can join them.
	turnStart := now.Add(-2 * time.Minute)
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-join-id",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: turnStart,
		EndedAt:   now,
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: turnStart, Turn: 1, TraceID: "trace_join", Attempt: 0, Attempts: 1, DurationMs: 900, TTFBMs: 40, TTFTMs: 120},
			{Type: "tool_call", Timestamp: turnStart, Turn: 1, Tool: "bash", Args: json.RawMessage(`{"cmd":"sleep 120"}`)},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "done", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// The trace is timestamped minutes after the turn; a timestamp-proximity
	// join would miss it, the ID join must not.
	trace := &debug.HTTPTrace{
		ID:            "trace_join",
		Timestamp:     now,
		SessionID:     sessionID,
		ProviderID:    "test-provider",
		Status:        200,
		DurationMs:    1100,
		RequestBytes:  100,
		ResponseBytes: 500,
		TTFBMs:        40,
		TTFTMs:        120,
		Attempt:       0,
		RunID:         "run-join-id",
		Turn:          1,
		Model:         "test-model",
		FinishReason:  "stop",
		Usage:         &debug.UsageTotals{PromptTokens: 400, CompletionTokens: 50, TotalTokens: 450},
	}
	writeTestTrace(t, dir, sessionID, trace)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}

	if assistant.LLMTraceID != "trace_join" {
		t.Errorf("LLMTraceID = %q, want trace_join", assistant.LLMTraceID)
	}
	if assistant.LLMDurationMs != 1100 {
		t.Errorf("LLMDurationMs = %d, want 1100 (from trace)", assistant.LLMDurationMs)
	}
	if assistant.LLMTTFBMs != 40 {
		t.Errorf("LLMTTFBMs = %d, want 40", assistant.LLMTTFBMs)
	}
	if assistant.LLMTTFTMs != 120 {
		t.Errorf("LLMTTFTMs = %d, want 120 (time to first token)", assistant.LLMTTFTMs)
	}
	if assistant.LLMUsage == nil || assistant.LLMUsage.PromptTokens != 400 {
		t.Errorf("LLMUsage = %+v, want prompt=400", assistant.LLMUsage)
	}
	if assistant.LLMFinishReason != "stop" {
		t.Errorf("LLMFinishReason = %q, want stop", assistant.LLMFinishReason)
	}
}

func TestGetReport_RetryAttemptsSurfaced(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-retry"
	now := time.Now().UTC()

	// The timeline records the successful attempt: it succeeded on the third
	// attempt (zero-based attempt 2), with 3 total attempts for the turn.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-retry",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now, Turn: 1, TraceID: "trace_ok", Attempt: 2, Attempts: 3, DurationMs: 800, TTFBMs: 30, TTFTMs: 90},
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "ok", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// Three traces for the same (run, turn): two failed attempts and one success.
	for i, status := range []int{503, 503, 200} {
		trace := &debug.HTTPTrace{
			ID:        debug.TraceID(fmt.Sprintf("trace_%d", i)),
			Timestamp: now,
			SessionID: sessionID,
			Status:    status,
			Attempt:   i,
			RunID:     "run-retry",
			Turn:      1,
		}
		writeTestTrace(t, dir, sessionID, trace)
	}
	// The success trace is the one referenced by the timeline.
	okTrace := &debug.HTTPTrace{
		ID:           "trace_ok",
		Timestamp:    now,
		SessionID:    sessionID,
		Status:       200,
		DurationMs:   800,
		Attempt:      2,
		RunID:        "run-retry",
		Turn:         1,
		Model:        "test-model",
		FinishReason: "stop",
		TTFBMs:       30,
		TTFTMs:       90,
	}
	writeTestTrace(t, dir, sessionID, okTrace)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}

	if assistant.LLMAttempt != 2 {
		t.Errorf("LLMAttempt = %d, want 2 (successful attempt)", assistant.LLMAttempt)
	}
	if assistant.LLMAttemptCount != 3 {
		t.Errorf("LLMAttemptCount = %d, want 3", assistant.LLMAttemptCount)
	}
	if assistant.LLMFailedAttempts != 2 {
		t.Errorf("LLMFailedAttempts = %d, want 2 (failed attempts before success)", assistant.LLMFailedAttempts)
	}
	if assistant.LLMTraceID != "trace_ok" {
		t.Errorf("LLMTraceID = %q, want trace_ok", assistant.LLMTraceID)
	}
	if assistant.LLMTTFTMs != 90 {
		t.Errorf("LLMTTFTMs = %d, want 90", assistant.LLMTTFTMs)
	}
}

func TestGetReport_TraceRunTurnGroupFallback(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-group-fb"
	now := time.Now().UTC()

	// No llm_call event in the timeline (e.g. persisted before the feature),
	// but the traces carry run/turn IDs that match this run.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-group",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "found", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	for i, status := range []int{503, 200} {
		trace := &debug.HTTPTrace{
			ID:        debug.TraceID(fmt.Sprintf("group_%d", i)),
			Timestamp: now.Add(time.Duration(i) * time.Minute), // far apart — timestamp join would fail
			SessionID: sessionID,
			Status:    status,
			Attempt:   i,
			RunID:     "run-group",
			Turn:      1,
		}
		writeTestTrace(t, dir, sessionID, trace)
	}

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}

	// The (run, turn) group should have matched the traces: the final attempt
	// wins the join and the group size is the attempt count.
	if assistant.LLMTraceID != "group_1" {
		t.Errorf("LLMTraceID = %q, want group_1 (highest attempt)", assistant.LLMTraceID)
	}
	if assistant.LLMAttempt != 1 {
		t.Errorf("LLMAttempt = %d, want 1", assistant.LLMAttempt)
	}
	if assistant.LLMAttemptCount != 2 {
		t.Errorf("LLMAttemptCount = %d, want 2", assistant.LLMAttemptCount)
	}
	if assistant.LLMFailedAttempts != 1 {
		t.Errorf("LLMFailedAttempts = %d, want 1", assistant.LLMFailedAttempts)
	}
}

func TestGetReport_TimestampHeuristicRemainsFallback(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-ts-fb"
	now := time.Now().UTC()

	// No llm_call events and traces without run/turn IDs: only the legacy
	// ±30s timestamp heuristic can join them.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-ts",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "ok", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	trace := &debug.HTTPTrace{
		ID:         "legacy_trace",
		Timestamp:  now.Add(2 * time.Second), // within the ±30s window
		SessionID:  sessionID,
		Status:     200,
		DurationMs: 500,
	}
	writeTestTrace(t, dir, sessionID, trace)

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var assistant *Turn
	for i := range rep.Turns {
		if rep.Turns[i].Role == "assistant" {
			assistant = &rep.Turns[i]
			break
		}
	}
	if assistant == nil {
		t.Fatal("expected an assistant turn")
	}
	if assistant.LLMTraceID != "legacy_trace" {
		t.Errorf("LLMTraceID = %q, want legacy_trace (timestamp fallback)", assistant.LLMTraceID)
	}
	if assistant.LLMDurationMs != 500 {
		t.Errorf("LLMDurationMs = %d, want 500", assistant.LLMDurationMs)
	}
}

// writeTestSessionSnapshot writes a session snapshot file (UISession schema) with
// the given messages and a session.json symlink, so GetReport can enrich the report.
func writeTestSessionSnapshot(t *testing.T, dir, sessionID string, msgs []message.Message) {
	t.Helper()
	now := time.Now().UTC()
	snap := session.UISession{
		ID:        sessionID,
		Title:     "Test Session",
		Workspace: "/tmp/test",
		Messages:  msgs,
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("failed to marshal session snapshot: %v", err)
	}
	fp := filepath.Join(dir, "sessions", sessionID, "2025-01-01T00-00-00.json")
	if err := os.MkdirAll(filepath.Dir(fp), 0o700); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	if err := os.WriteFile(fp, data, 0o600); err != nil {
		t.Fatalf("failed to write session snapshot: %v", err)
	}
	symlink := filepath.Join(dir, "sessions", sessionID, "session.json")
	os.Remove(symlink)
	if err := os.Symlink("2025-01-01T00-00-00.json", symlink); err != nil {
		t.Fatalf("failed to create session symlink: %v", err)
	}
}

// userTurnContents returns the content of every user-role card in report order.
func userTurnContents(t *testing.T, rep *SessionReport) []string {
	t.Helper()
	var out []string
	for _, turn := range rep.Turns {
		if turn.Role == "user" {
			out = append(out, turn.Content)
		}
	}
	return out
}

// assistantTurnContents returns {turn number, content} pairs for assistant cards in order.
func assistantTurnContents(t *testing.T, rep *SessionReport) []int {
	t.Helper()
	var out []int
	for _, turn := range rep.Turns {
		if turn.Role == "assistant" {
			out = append(out, turn.Turn)
		}
	}
	return out
}

func TestGetReport_UserMessagesAttributedByTimestamp(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-user-ts"
	now := time.Now().UTC()

	// Two assistant turns emitted at t=15 and t=25. The snapshot stores the
	// user messages in a different array order than the chronological turn
	// order (e.g. after re-compaction): "Q2"@20 appears before "Q1"@10.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-user-ts",
		SessionID: sessionID,
		StartedAt: now.Add(-1 * time.Minute),
		EndedAt:   now,
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now.Add(15 * time.Second), Turn: 1, DurationMs: 100, TraceID: "t1"},
			{Type: "tool_call", Timestamp: now.Add(15 * time.Second), Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now.Add(15 * time.Second), Turn: 1, Tool: "read", Output: "a", Error: false},
			{Type: "llm_call", Timestamp: now.Add(25 * time.Second), Turn: 2, DurationMs: 100, TraceID: "t2"},
			{Type: "tool_call", Timestamp: now.Add(25 * time.Second), Turn: 2, Tool: "grep"},
			{Type: "tool_result", Timestamp: now.Add(25 * time.Second), Turn: 2, Tool: "grep", Output: "b", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// Snapshot array order diverges from turn chronology: Q1@10 belongs to
	// turn 1 (emitted 15s), Q2@20 belongs to turn 2 (emitted 25s).
	writeTestSessionSnapshot(t, dir, sessionID, []message.Message{
		{Role: "user", Content: "Q2", CreatedAt: now.Add(20 * time.Second)},
		{Role: "assistant", Content: "Answer 2", CreatedAt: now.Add(26 * time.Second)},
		{Role: "user", Content: "Q1", CreatedAt: now.Add(10 * time.Second)},
		{Role: "assistant", Content: "Answer 1", CreatedAt: now.Add(16 * time.Second)},
	})

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Report emission order is turn 1 then turn 2.
	if got := assistantTurnContents(t, rep); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("expected assistant turns [1 2], got %v", got)
	}

	// User message must be attributed by timestamp to the turn that
	// immediately follows it chronologically, not by snapshot array order.
	users := userTurnContents(t, rep)
	if len(users) != 2 {
		t.Fatalf("expected 2 user cards, got %d: %v", len(users), users)
	}
	if users[0] != "Q1" {
		t.Errorf("first user card content = %q, want %q (Q1 precedes turn 1)", users[0], "Q1")
	}
	if users[1] != "Q2" {
		t.Errorf("second user card content = %q, want %q (Q2 precedes turn 2)", users[1], "Q2")
	}
}

func TestGetReport_UserCardTimestampComesFromMatchedMessage(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-user-ts-flip"
	now := time.Now().UTC()

	// Two assistant turns emitted at 10s and 20s. A user message "Q-late" has a
	// created_at (30s) that inverts — it is later than every turn's emitted time
	// (e.g. a sub-agent collect timestamped after the run). No turn is at/after
	// it, so snapshot array order is the tie-break and it lands on the earliest
	// remaining card. Its displayed timestamp must be its own created_at, not
	// the turn's emitted time.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-user-flip",
		SessionID: sessionID,
		StartedAt: now.Add(-1 * time.Minute),
		EndedAt:   now,
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now.Add(10 * time.Second), Turn: 1, DurationMs: 100, TraceID: "t1"},
			{Type: "tool_call", Timestamp: now.Add(10 * time.Second), Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now.Add(10 * time.Second), Turn: 1, Tool: "read", Output: "a", Error: false},
			{Type: "llm_call", Timestamp: now.Add(20 * time.Second), Turn: 2, DurationMs: 100, TraceID: "t2"},
			{Type: "tool_call", Timestamp: now.Add(20 * time.Second), Turn: 2, Tool: "grep"},
			{Type: "tool_result", Timestamp: now.Add(20 * time.Second), Turn: 2, Tool: "grep", Output: "b", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// Q-late@30s is later than every turn; Q-norm@15s pairs cleanly with turn
	// 2 (20s). Array order tie-break places Q-late (first in the snapshot) on
	// the earliest remaining turn, turn 1.
	writeTestSessionSnapshot(t, dir, sessionID, []message.Message{
		{Role: "user", Content: "Q-late", CreatedAt: now.Add(30 * time.Second)},
		{Role: "assistant", Content: "Answer 1", CreatedAt: now.Add(11 * time.Second)},
		{Role: "user", Content: "Q-norm", CreatedAt: now.Add(15 * time.Second)},
		{Role: "assistant", Content: "Answer 2", CreatedAt: now.Add(21 * time.Second)},
	})

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	users := userTurnContents(t, rep)
	if len(users) != 2 {
		t.Fatalf("expected 2 user cards, got %d: %v", len(users), users)
	}
	if users[0] != "Q-late" {
		t.Errorf("first user card content = %q, want Q-late (array-order tie-break for inverted timestamp)", users[0])
	}
	if users[1] != "Q-norm" {
		t.Errorf("second user card content = %q, want Q-norm", users[1])
	}

	// The displayed timestamp on the first user card must be the matched
	// message's created_at (30s), not the turn's emitted time (10s).
	u1 := rep.Turns[0]
	if u1.Content != "Q-late" || !u1.Timestamp.Equal(now.Add(30*time.Second)) {
		t.Errorf("turn 1 user card timestamp = %v, want 30s (from matched message)", u1.Timestamp)
	}
}

func TestGetReport_UserTimestampTieBrokenByArrayOrder(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-user-tie"
	now := time.Now().UTC()

	// Two user messages share identical created_at timestamps (a tie). Snapshot
	// array order must win: first Q appears on turn 1's card, second on turn 2's.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-user-tie",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now.Add(1 * time.Second), Turn: 1, DurationMs: 100, TraceID: "a"},
			{Type: "tool_call", Timestamp: now.Add(1 * time.Second), Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now.Add(1 * time.Second), Turn: 1, Tool: "read", Output: "a", Error: false},
			{Type: "llm_call", Timestamp: now.Add(2 * time.Second), Turn: 2, DurationMs: 100, TraceID: "b"},
			{Type: "tool_call", Timestamp: now.Add(2 * time.Second), Turn: 2, Tool: "grep"},
			{Type: "tool_result", Timestamp: now.Add(2 * time.Second), Turn: 2, Tool: "grep", Output: "b", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// Both user messages created_at = 1s, a tie with turn 1's emission. Array
	// order decides: the first goes to turn 1, the second to turn 2.
	writeTestSessionSnapshot(t, dir, sessionID, []message.Message{
		{Role: "user", Content: "First", CreatedAt: now.Add(1 * time.Second)},
		{Role: "assistant", Content: "Answer 1", CreatedAt: now.Add(1 * time.Second)},
		{Role: "user", Content: "Second", CreatedAt: now.Add(1 * time.Second)},
		{Role: "assistant", Content: "Answer 2", CreatedAt: now.Add(2 * time.Second)},
	})

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	users := userTurnContents(t, rep)
	if len(users) != 2 {
		t.Fatalf("expected 2 user cards, got %d: %v", len(users), users)
	}
	if users[0] != "First" {
		t.Errorf("first user card = %q, want First (array-order tie-break)", users[0])
	}
	if users[1] != "Second" {
		t.Errorf("second user card = %q, want Second", users[1])
	}
}

func TestGetReport_DropsEmptyPlaceholderUserCards(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-drop-empty-user"
	now := time.Now().UTC()

	// Two assistant turns emitted at t=10 and t=20. The snapshot stores only
	// ONE user message ("Q1"), matching turn 1. There is no user message to
	// attribute to turn 2 (e.g. after compaction or a turn with no preceding
	// prompt), so turn 2's synthetic placeholder card must be dropped instead
	// of rendering as an empty one-line card.
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-drop-user",
		SessionID: sessionID,
		StartedAt: now.Add(-1 * time.Minute),
		EndedAt:   now,
		Termination: &timeline.TimelineTermination{
			Reason: timeline.TerminationCompleted,
		},
		Events: []timeline.TimelineEvent{
			{Type: "llm_call", Timestamp: now.Add(10 * time.Second), Turn: 1, DurationMs: 100, TraceID: "t1"},
			{Type: "tool_call", Timestamp: now.Add(10 * time.Second), Turn: 1, Tool: "read"},
			{Type: "tool_result", Timestamp: now.Add(10 * time.Second), Turn: 1, Tool: "read", Output: "a", Error: false},
			{Type: "llm_call", Timestamp: now.Add(20 * time.Second), Turn: 2, DurationMs: 100, TraceID: "t2"},
			{Type: "tool_call", Timestamp: now.Add(20 * time.Second), Turn: 2, Tool: "grep"},
			{Type: "tool_result", Timestamp: now.Add(20 * time.Second), Turn: 2, Tool: "grep", Output: "b", Error: false},
		},
	}
	writeTestTimeline(t, dir, sessionID, tl)

	// Only one user message exists; it pairs with turn 1 (emitted 10s).
	writeTestSessionSnapshot(t, dir, sessionID, []message.Message{
		{Role: "user", Content: "Q1", CreatedAt: now.Add(5 * time.Second)},
		{Role: "assistant", Content: "Answer 1", CreatedAt: now.Add(11 * time.Second)},
		{Role: "assistant", Content: "Answer 2", CreatedAt: now.Add(21 * time.Second)},
	})

	rep, err := svc.GetReport(sessionID, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only one user card may remain, and it must carry real matched content.
	users := userTurnContents(t, rep)
	if len(users) != 1 {
		t.Fatalf("expected 1 rendered user card, got %d: %v", len(users), users)
	}
	if users[0] != "Q1" {
		t.Errorf("rendered user card content = %q, want Q1", users[0])
	}

	// Every rendered user card must contain real, non-empty content.
	for _, turn := range rep.Turns {
		if turn.Role == "user" && turn.Content == "" {
			t.Errorf("rendered empty user card on turn %d", turn.Turn)
		}
	}

	// Both assistant cards must still be present after the placeholder drop.
	if got := assistantTurnContents(t, rep); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("expected assistant turns [1 2] after drop, got %v", got)
	}
}
