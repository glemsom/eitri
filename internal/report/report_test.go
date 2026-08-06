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
