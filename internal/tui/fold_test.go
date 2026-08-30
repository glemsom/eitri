package tui

import "testing"

// Stream deltas fold through the Fold: the streaming assistant message grows,
// and each delta lands on the live timeline in arrival order with sequence
// numbers stamped by the Fold alone.
func TestFoldStreamGrowsMessageAndTimeline(t *testing.T) {
	s := NewTurnSession(stubTurn("done", nil))
	tx := newTestTx()
	tx.busy = true
	f := NewFold(s)

	f.Stream(&tx, ReasoningStream, "think")
	f.Stream(&tx, AnswerStream, "answer")

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
	if len(s.LiveTimeline()) != 2 {
		t.Fatalf("timeline = %d events, want 2", len(s.LiveTimeline()))
	}
	if s.LiveTimeline()[0].Kind != EventReasoning || s.LiveTimeline()[1].Kind != EventAnswer {
		t.Errorf("event kinds = %v/%v, want reasoning/answer", s.LiveTimeline()[0].Kind, s.LiveTimeline()[1].Kind)
	}
	if s.LiveTimeline()[0].Seq != 0 || s.LiveTimeline()[1].Seq != 1 {
		t.Errorf("seqs = %d/%d, want 0/1", s.LiveTimeline()[0].Seq, s.LiveTimeline()[1].Seq)
	}
}

// An empty delta folds to nothing: no message appended, no timeline event.
func TestFoldStreamEmptyDeltaNoop(t *testing.T) {
	s := NewTurnSession(stubTurn("done", nil))
	tx := newTestTx()
	tx.busy = true
	f := NewFold(s)

	f.Stream(&tx, AnswerStream, "")

	if len(tx.messages) != 0 || len(s.LiveTimeline()) != 0 {
		t.Fatalf("empty delta must not touch transcript; messages=%d timeline=%d", len(tx.messages), len(s.LiveTimeline()))
	}
}

// A turn that starts after a committed streaming message appends a fresh
// streaming message rather than growing the old one.
func TestFoldNewTurnAppendsFreshStreamingMessage(t *testing.T) {
	s := NewTurnSession(stubTurn("done", nil))
	tx := newTestTx()
	tx.busy = true
	f := NewFold(s)

	f.Stream(&tx, AnswerStream, "first")
	tx.messages[0].streaming = false
	s.curStream = -1
	s.flow.Reset()
	s.order = nil
	s.tools = nil

	f.Stream(&tx, AnswerStream, "second")

	if len(tx.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tx.messages))
	}
	if !tx.messages[1].streaming || tx.messages[1].content != "second" {
		t.Errorf("second message = %+v, want fresh streaming message with %q", tx.messages[1], "second")
	}
}

// Tool observations fold through the Fold into both the tool log and the live
// timeline, in arrival order.
func TestFoldToolRoutesToLogAndTimeline(t *testing.T) {
	s := NewTurnSession(stubTurn("done", nil))
	tx := newTestTx()
	tx.busy = true
	f := NewFold(s)

	f.Tool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: "ls"}})

	if tx.log.Len() != 1 {
		t.Fatalf("tool log = %d entries, want 1", tx.log.Len())
	}
	if len(s.LiveTimeline()) != 1 || s.LiveTimeline()[0].Kind != EventToolStart {
		t.Fatalf("timeline = %+v, want one tool-start event", s.LiveTimeline())
	}
	if s.LiveTimeline()[0].Seq != 0 {
		t.Errorf("seq = %d, want 0", s.LiveTimeline()[0].Seq)
	}

	f.Tool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "out"}})

	if len(s.LiveTimeline()) != 2 || s.LiveTimeline()[1].Kind != EventToolResult {
		t.Fatalf("timeline after result = %+v, want tool-result second", s.LiveTimeline())
	}
}

// applyTool folds a tool observation through a Fold bound to a throwaway
// disarmed session: Tool never reads the stream cursor or thinking flag, so
// the binding is inert and the call-sites below stay one line.
func applyTool(tx *Transcript, u ToolUpdate) {
	NewFold(NewTurnSession(nil)).Tool(tx, u)
}
