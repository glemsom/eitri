package runner

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runstate"
)

// TestPersistRunTimeline_NoRunState verifies the timeline-writing path is
// callable for any session without a UI RunState (issue #1038): it takes the
// run ID and start time explicitly, so headless (batch) runs can persist
// timelines exactly like browser runs.
func TestPersistRunTimeline_NoRunState(t *testing.T) {
	persister, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	svc := NewRunService(RunServiceDeps{Persister: persister})

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)
	w.SetTurn(1)
	w.ToolCall("grep", map[string]any{"pattern": "foo"})
	w.ToolResult("grep", "some output")
	w.Done("msg_1", runstate.EstimateUsage("hi", nil, "test-model"))

	startedAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.persistRunTimeline("session-1", "run-abc", startedAt, sseState, RunConfig{
		ModelName:  "test-model",
		ProviderID: "opencode_go",
	}, &runstate.TimelineTermination{
		Reason:  runstate.TerminationCompleted,
		Message: "",
	})

	metas, err := persister.ListTimelines("session-1")
	if err != nil {
		t.Fatalf("ListTimelines: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s), want 1", len(metas))
	}

	data, err := persister.LoadTimeline("session-1", metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	var tl runstate.Timeline
	if err := json.Unmarshal(data, &tl); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}

	if tl.RunID != "run-abc" {
		t.Errorf("RunID = %q, want %q", tl.RunID, "run-abc")
	}
	if tl.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want %q", tl.SessionID, "session-1")
	}
	if !tl.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v, want %v", tl.StartedAt, startedAt)
	}
	if tl.Provider.Model != "test-model" {
		t.Errorf("Provider.Model = %q, want %q", tl.Provider.Model, "test-model")
	}
	if tl.Provider.ProviderID != "opencode_go" {
		t.Errorf("Provider.ProviderID = %q, want %q", tl.Provider.ProviderID, "opencode_go")
	}
	if tl.Termination == nil || tl.Termination.Reason != runstate.TerminationCompleted {
		t.Errorf("Termination = %+v, want reason %q", tl.Termination, runstate.TerminationCompleted)
	}
	if len(tl.Events) != 3 {
		t.Fatalf("got %d timeline events, want 3", len(tl.Events))
	}
	if tl.Events[0].Type != "tool_call" || tl.Events[0].Tool != "grep" {
		t.Errorf("first event = %+v, want tool_call grep", tl.Events[0])
	}
	if tl.Events[2].Type != "done" {
		t.Errorf("last event type = %q, want %q", tl.Events[2].Type, "done")
	}
}

// TestPersistRunTimeline_AllTerminationReasons verifies the timeline path
// records the correct termination reason on each exit path without a RunState.
func TestPersistRunTimeline_AllTerminationReasons(t *testing.T) {
	reasons := []runstate.TerminationReason{
		runstate.TerminationCompleted,
		runstate.TerminationCancelled,
		runstate.TerminationMaxTurns,
		runstate.TerminationError,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			persister, err := persist.New(t.TempDir())
			if err != nil {
				t.Fatalf("persist.New: %v", err)
			}
			svc := NewRunService(RunServiceDeps{Persister: persister})

			sseState := runstate.New()
			svc.persistRunTimeline("session-1", "run-1", time.Now(), sseState, RunConfig{
				ModelName:  "test-model",
				ProviderID: "opencode_go",
			}, &runstate.TimelineTermination{Reason: reason})

			metas, err := persister.ListTimelines("session-1")
			if err != nil {
				t.Fatalf("ListTimelines: %v", err)
			}
			if len(metas) != 1 {
				t.Fatalf("got %d timeline(s), want 1", len(metas))
			}
			data, err := persister.LoadTimeline("session-1", metas[0].Filename)
			if err != nil {
				t.Fatalf("LoadTimeline: %v", err)
			}
			var tl runstate.Timeline
			if err := json.Unmarshal(data, &tl); err != nil {
				t.Fatalf("unmarshal timeline: %v", err)
			}
			if tl.Termination == nil || tl.Termination.Reason != reason {
				t.Errorf("Termination = %+v, want reason %q", tl.Termination, reason)
			}
		})
	}
}

// TestPersistRunTimeline_NilPersisterIsNoop verifies the timeline path is a
// no-op when no persister is configured (matching UI behaviour).
func TestPersistRunTimeline_NilPersisterIsNoop(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})
	sseState := runstate.New()
	// Must not panic and must not write anything.
	svc.persistRunTimeline("session-1", "run-1", time.Now(), sseState, RunConfig{
		ModelName:  "test-model",
		ProviderID: "opencode_go",
	}, &runstate.TimelineTermination{Reason: runstate.TerminationCompleted})
}
