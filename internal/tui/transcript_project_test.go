package tui

import "testing"

// Stream outcomes grow the assistant message and append arrival-ordered live
// timeline events with sequence numbers.
func TestTranscriptProjectStreamGrowsMessageAndTimeline(t *testing.T) {
	tx := newTestTx()
	tx.busy = true

	projectStream(&tx, ReasoningStream, "think")
	projectStream(&tx, AnswerStream, "answer")

	if len(tx.messages) != 1 {
		t.Fatalf("expected 1 streaming message, got %d", len(tx.messages))
	}
	m := tx.messages[0]
	if m.role != "eitri" || !m.streaming {
		t.Fatalf("message = %+v, want a streaming eitri message", m)
	}
	if m.reasoning != "think" || m.content != "answer" {
		t.Errorf("snapshots reasoning=%q content=%q, want %q/%q", m.reasoning, m.content, "think", "answer")
	}
	if len(tx.LiveTimeline()) != 2 {
		t.Fatalf("timeline = %d events, want 2", len(tx.LiveTimeline()))
	}
	if tx.LiveTimeline()[0].Kind != EventReasoning || tx.LiveTimeline()[1].Kind != EventAnswer {
		t.Errorf("event kinds = %v/%v, want reasoning/answer", tx.LiveTimeline()[0].Kind, tx.LiveTimeline()[1].Kind)
	}
	if tx.LiveTimeline()[0].Seq != 0 || tx.LiveTimeline()[1].Seq != 1 {
		t.Errorf("seqs = %d/%d, want 0/1", tx.LiveTimeline()[0].Seq, tx.LiveTimeline()[1].Seq)
	}
}

// An empty stream outcome changes nothing: no message appended, no timeline event.
func TestTranscriptProjectEmptyStreamNoop(t *testing.T) {
	tx := newTestTx()
	tx.busy = true

	projectStream(&tx, AnswerStream, "")

	if len(tx.messages) != 0 || len(tx.LiveTimeline()) != 0 {
		t.Fatalf("empty delta must not touch transcript; messages=%d timeline=%d", len(tx.messages), len(tx.LiveTimeline()))
	}
}

// A turn that starts after a committed streaming message appends a fresh
// streaming message rather than growing the old one.
func TestTranscriptProjectNewTurnAppendsFreshStreamingMessage(t *testing.T) {
	tx := newTestTx()
	tx.busy = true

	projectStream(&tx, AnswerStream, "first")
	tx.messages[0].streaming = false
	tx.curStream = -1
	tx.flow.Reset()

	projectStream(&tx, AnswerStream, "second")

	if len(tx.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tx.messages))
	}
	if !tx.messages[1].streaming || tx.messages[1].content != "second" {
		t.Errorf("second message = %+v, want fresh streaming message with %q", tx.messages[1], "second")
	}
}

// Tool outcomes update both the tool log and the live timeline in arrival order.
func TestTranscriptProjectToolRoutesToLogAndTimeline(t *testing.T) {
	tx := newTestTx()
	tx.busy = true

	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: "ls"}})

	if tx.log.Len() != 1 {
		t.Fatalf("tool log = %d entries, want 1", tx.log.Len())
	}
	if len(tx.LiveTimeline()) != 1 || tx.LiveTimeline()[0].Kind != EventToolStart {
		t.Fatalf("timeline = %+v, want one tool-start event", tx.LiveTimeline())
	}
	if tx.LiveTimeline()[0].Seq != 0 {
		t.Errorf("seq = %d, want 0", tx.LiveTimeline()[0].Seq)
	}

	projectTool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "out"}})

	if len(tx.LiveTimeline()) != 2 || tx.LiveTimeline()[1].Kind != EventToolResult {
		t.Fatalf("timeline after result = %+v, want tool-result second", tx.LiveTimeline())
	}
}
