package tui

import (
	"errors"
	"testing"
)

func TestTranscriptProjectProjectsLiveTurnOutcomes(t *testing.T) {
	tx := newTestTx()

	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "inspect", ThinkingEnabled: false}})

	if !tx.busy || len(tx.messages) != 1 || tx.messages[0].content != "inspect" {
		t.Fatalf("start projection = busy %v, messages %+v", tx.busy, tx.messages)
	}
	tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: "consider"}})
	tx.Project(TranscriptOutcome{Tool: &ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"pwd"}`}}})
	tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "done"}})

	last := tx.messages[len(tx.messages)-1]
	if last.reasoning != "consider" || last.content != "done" || !last.streaming {
		t.Fatalf("stream snapshots = %+v", last)
	}
	if got := tx.log.Len(); got != 1 {
		t.Fatalf("tool entries = %d, want 1", got)
	}
	if got := tx.log.entries[0].anchor; got != 0 {
		t.Fatalf("tool anchor = %d, want prompt index 0", got)
	}
	events := tx.LiveTimeline()
	if len(events) != 3 || events[0].Kind != EventReasoning || events[1].Kind != EventToolStart || events[2].Kind != EventAnswer {
		t.Fatalf("timeline = %+v", events)
	}
	if tx.busyPulse == 0 {
		t.Fatal("tool start did not arm the thinking-off busy pulse")
	}
	if !tx.layout.dirty {
		t.Fatal("outcome projection did not invalidate layout")
	}
}

func TestTranscriptProjectCompletesSuccessfulRun(t *testing.T) {
	tx := newTestTx()
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "inspect", ThinkingEnabled: true}})
	tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: "partial reasoning"}})
	tx.Project(TranscriptOutcome{Tool: &ToolUpdate{Start: &ToolStart{Name: "bash"}}})
	tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial answer"}})

	stopped, err := tx.Project(TranscriptOutcome{Complete: &TranscriptCompletion{Answer: "final answer", Reasoning: "final reasoning"}})
	if stopped || err != nil {
		t.Fatalf("completion = stopped %v, err %v; want false, nil", stopped, err)
	}
	if tx.busy {
		t.Fatal("successful completion left transcript busy")
	}
	msg := tx.messages[len(tx.messages)-1]
	if msg.content != "final answer" || msg.reasoning != "final reasoning" || msg.streaming {
		t.Fatalf("completed assistant message = %+v", msg)
	}
	if len(msg.events) != 3 || msg.events[0].Kind != EventReasoning || msg.events[1].Kind != EventToolStart || msg.events[2].Kind != EventAnswer {
		t.Fatalf("completed event ordering = %+v", msg.events)
	}
	if _, ok := msg.expansion.forceFor(blockReasoning, 0); ok {
		t.Fatalf("completed message retained live reasoning fragment force: %+v", msg.expansion)
	}
}

func TestTranscriptProjectCompletesFailedAndStoppedRuns(t *testing.T) {
	t.Run("failure preserves streamed output then appends failure", func(t *testing.T) {
		tx := newTestTx()
		tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "inspect"}})
		tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial"}})

		stopped, err := tx.Project(TranscriptOutcome{Complete: &TranscriptCompletion{Err: errTranscriptOutcome}})
		if stopped || err != errTranscriptOutcome {
			t.Fatalf("completion = stopped %v, err %v", stopped, err)
		}
		if tx.busy || len(tx.messages) != 3 || tx.messages[1].content != "partial" || tx.messages[1].streaming {
			t.Fatalf("failed projection messages = %+v", tx.messages)
		}
		if tx.messages[2].content != failurePrefix()+errTranscriptOutcome.Error() {
			t.Fatalf("failure message = %+v", tx.messages[2])
		}
	})
	t.Run("stop retains partial output and cleans reasoning fragments", func(t *testing.T) {
		tx := newTestTx()
		tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "inspect", ThinkingEnabled: true}})
		tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: "partial reasoning"}})
		tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial answer"}})

		stopped, err := tx.Project(TranscriptOutcome{Complete: &TranscriptCompletion{Stopped: true}})
		if !stopped || err != nil {
			t.Fatalf("completion = stopped %v, err %v", stopped, err)
		}
		msg := tx.messages[len(tx.messages)-1]
		if tx.busy || msg.content != "partial answer" || msg.reasoning != "partial reasoning" || !msg.stopped || msg.streaming {
			t.Fatalf("stopped message = %+v", msg)
		}
		if _, ok := msg.expansion.forceFor(blockReasoning, 0); ok {
			t.Fatalf("stopped message retained live reasoning fragment force: %+v", msg.expansion)
		}
	})
}

var errTranscriptOutcome = errors.New("provider failed")
