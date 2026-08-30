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
