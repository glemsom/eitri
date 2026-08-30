package tui

import "testing"

func TestTurnFlowObserveBuildsLiveSnapshotsAndEventLog(t *testing.T) {
	t.Parallel()
	var flow TurnFlow

	flow.Observe(ReasoningStream, "think ")
	flow.Observe(AnswerStream, "ans")
	flow.Observe(AnswerStream, "wer")

	if got := flow.Reasoning(); got != "think " {
		t.Fatalf("Reasoning = %q, want %q", got, "think ")
	}
	if got := flow.Content(); got != "answer" {
		t.Fatalf("Content = %q, want %q", got, "answer")
	}
	events := flow.Events()
	if len(events) != 3 {
		t.Fatalf("Events = %d, want 3", len(events))
	}
	wantKinds := []EventKind{EventReasoning, EventAnswer, EventAnswer}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d kind = %v, want %v", i, events[i].Kind, want)
		}
		if events[i].Seq != i {
			t.Errorf("event %d seq = %d, want %d", i, events[i].Seq, i)
		}
	}
}

// A turn interleaving reasoning, a tool call, and answer text records all of
// it in one arrival-ordered event log; tool observations delimit the
// reasoning/answer fragments but never touch the streamed-text snapshots.
func TestTurnFlowObserveToolInterleavesWithStreamObservationsInArrivalOrder(t *testing.T) {
	t.Parallel()
	var flow TurnFlow

	flow.Observe(ReasoningStream, "checking repo")
	flow.ObserveTool(TimelineEvent{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	flow.ObserveTool(TimelineEvent{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "a.go", Lines: 1}})
	flow.Observe(AnswerStream, "done")

	if got := flow.Reasoning(); got != "checking repo" {
		t.Fatalf("Reasoning = %q, want %q", got, "checking repo")
	}
	if got := flow.Content(); got != "done" {
		t.Fatalf("Content = %q, want %q", got, "done")
	}
	events := flow.Events()
	if len(events) != 4 {
		t.Fatalf("Events = %d, want 4", len(events))
	}
	wantKinds := []EventKind{EventReasoning, EventToolStart, EventToolResult, EventAnswer}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d kind = %v, want %v", i, events[i].Kind, want)
		}
		if events[i].Seq != i {
			t.Errorf("event %d seq = %d, want %d", i, events[i].Seq, i)
		}
	}
	if events[1].Start == nil || events[1].Start.Name != "bash" {
		t.Errorf("tool start event = %+v, want bash start payload", events[1])
	}
	if events[2].Result == nil || events[2].Result.Name != "bash" {
		t.Errorf("tool result event = %+v, want bash result payload", events[2])
	}
}

func TestTurnFlowFinalizeUsesProviderFinalSnapshotsForCompletedTurn(t *testing.T) {
	t.Parallel()
	var flow TurnFlow

	flow.Observe(ReasoningStream, "thin")
	flow.Observe(AnswerStream, "part")

	content, reasoning := flow.Finalize("partial answer", "thinking", false)
	if content != "partial answer" {
		t.Fatalf("content = %q, want %q", content, "partial answer")
	}
	if reasoning != "thinking" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "thinking")
	}
}

func TestTurnFlowFinalizeStoppedTurnKeepsLivePartialSnapshots(t *testing.T) {
	t.Parallel()
	var flow TurnFlow

	flow.Observe(ReasoningStream, "live thought")
	flow.Observe(AnswerStream, "live partial")

	content, reasoning := flow.Finalize("provider partial", "provider thought", true)
	if content != "live partial" {
		t.Fatalf("content = %q, want %q", content, "live partial")
	}
	if reasoning != "live thought" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "live thought")
	}
}

func TestTurnFlowFinalizeStoppedTurnUsesLongerProviderPrefix(t *testing.T) {
	t.Parallel()
	var flow TurnFlow

	flow.Observe(AnswerStream, "partial ")

	content, reasoning := flow.Finalize("partial more", "", true)
	if content != "partial more" {
		t.Fatalf("content = %q, want %q", content, "partial more")
	}
	if reasoning != "" {
		t.Fatalf("reasoning = %q, want empty", reasoning)
	}
}
