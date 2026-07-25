package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runstate"
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
func writeTestTimeline(t *testing.T, dir, sessionID string, tl *runstate.Timeline) {
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

	tl := &runstate.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &runstate.TimelineTermination{
			Reason:  runstate.TerminationCompleted,
			Message: "",
		},
		Events: []runstate.TimelineEvent{
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
	if runs[0].Termination.Reason != runstate.TerminationCompleted {
		t.Errorf("expected completed, got %s", runs[0].Termination.Reason)
	}
}

func TestListRuns_MultipleTimelines(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-multi"
	now := time.Now().UTC()

	// Write two timelines with different start times
	tl1 := &runstate.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &runstate.TimelineTermination{
			Reason: runstate.TerminationCompleted,
		},
		Events: []runstate.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
		},
	}
	tl2 := &runstate.Timeline{
		Version:   1,
		RunID:     "run-2",
		SessionID: sessionID,
		StartedAt: now.Add(30 * time.Second),
		EndedAt:   now.Add(45 * time.Second),
		Termination: &runstate.TimelineTermination{
			Reason: runstate.TerminationCancelled,
			Message: "user cancelled",
		},
		Events: []runstate.TimelineEvent{
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
	if runs[0].Termination.Reason != runstate.TerminationCompleted {
		t.Errorf("expected first run completed, got %s", runs[0].Termination.Reason)
	}
	if runs[1].Termination.Reason != runstate.TerminationCancelled {
		t.Errorf("expected second run cancelled, got %s", runs[1].Termination.Reason)
	}
}

func TestGetReport_FullTimeline(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-full"
	now := time.Now().UTC()

	tl := &runstate.Timeline{
		Version:   1,
		RunID:     "run-full",
		SessionID: sessionID,
		Provider: runstate.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: now,
		EndedAt:   now.Add(30 * time.Second),
		Termination: &runstate.TimelineTermination{
			Reason:  runstate.TerminationError,
			Message: "something went wrong",
		},
		Events: []runstate.TimelineEvent{
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
	if rep.Termination == nil || rep.Termination.Reason != runstate.TerminationError {
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
		Messages: []llm.Message{
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
	if rep.Summary.Note != "limited data — no timeline persisted for this session" {
		t.Errorf("expected note about limited data, got %q", rep.Summary.Note)
	}
	if len(rep.Turns) == 0 {
		t.Fatal("expected at least 1 turn in reconstructed report")
	}
}

func TestGetReport_RunIndexOutOfRange(t *testing.T) {
	svc, dir := newTestService(t)
	sessionID := "sess-range"
	now := time.Now().UTC()

	tl := &runstate.Timeline{
		Version:   1,
		RunID:     "run-1",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Events:    []runstate.TimelineEvent{},
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

	tl := &runstate.Timeline{
		Version:   1,
		RunID:     "run-fail",
		SessionID: sessionID,
		StartedAt: now,
		EndedAt:   now.Add(30 * time.Second),
		Termination: &runstate.TimelineTermination{
			Reason: runstate.TerminationCompleted,
		},
		Events: []runstate.TimelineEvent{
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
