package tui

import (
	"strings"
	"testing"
)

func TestModel_eventFeedRoutesStreamDelta(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	nm, _ := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "Hello"}}})
	m = asModel(t, nm)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "Hello" {
		t.Errorf("event-feed stream delta content = %q, want %q", got, "Hello")
	}
	if !m.tx.layout.dirty {
		t.Error("event-feed stream delta must mark the transcript layout dirty")
	}
}

func TestModel_eventFeedRoutesReasoningPulse(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	nm, _ := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: "think"}}})
	m = asModel(t, nm)
	n := m.tx.messages[len(m.tx.messages)-1]
	if n.reasoning != "think" || n.content != "" {
		t.Errorf("event-feed reasoning delta = reasoning %q content %q, want think/empty", n.reasoning, n.content)
	}
	if !strings.Contains(ansiStrip(view(m)), "think") {
		t.Errorf("event-feed reasoning should render the live thinking block, got: %q", view(m))
	}
}

func TestModel_eventFeedRoutesToolUpdate(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	nm, _ := m.Update(eventMsg{update: Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}}}})
	m = asModel(t, nm)
	if m.tx.log.Len() != 1 {
		t.Fatalf("event-feed tool update must fold into the tool log, got %d entries", m.tx.log.Len())
	}
}

func TestModel_eventFeedIgnoresStaleRunEvents(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	nm, _ := m.Update(eventMsg{update: Event{RunID: 41, Stream: &StreamUpdate{Kind: AnswerStream, Delta: "stale"}}})
	m = asModel(t, nm)
	if len(m.tx.messages) != 1 || m.tx.messages[0].role != "you" {
		t.Fatalf("pre-start run event leaked assistant into transcript: %+v", m.tx.messages)
	}

	nm, _ = m.Update(eventMsg{update: Event{RunID: 42, TurnStart: true}})
	m = asModel(t, nm)
	nm, _ = m.Update(eventMsg{update: Event{RunID: 42, Stream: &StreamUpdate{Kind: AnswerStream, Delta: "live"}}})
	m = asModel(t, nm)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "live" {
		t.Fatalf("active run event content = %q, want live", got)
	}

	nm, _ = m.Update(eventMsg{update: Event{RunID: 41, Stream: &StreamUpdate{Kind: AnswerStream, Delta: "old"}}})
	m = asModel(t, nm)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "live" {
		t.Fatalf("stale run event mutated transcript, got %q", got)
	}
}

// Completion must wait for an event already removed by the feed waiter. Bubble
// Tea may deliver the completion before that queued event message, despite the
// feed having observed the event first.
func TestModel_eventFeedProjectsRemovedEventBeforeCompletion(t *testing.T) {
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{Turn: streamingTurn, Events: feed, Config: cfgFixture()})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "queued"}}
	removed := eventWait(feed)()
	if _, ok := removed.(eventMsg); !ok {
		t.Fatalf("feed waiter message = %T, want eventMsg", removed)
	}

	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "final"})
	m = asModel(t, nm)
	if !m.tx.busy {
		t.Fatal("completion committed before the removed feed event was projected")
	}

	nm, _ = m.Update(removed)
	m = asModel(t, nm)
	if m.tx.busy {
		t.Fatal("completion did not commit after the removed feed event was projected")
	}
	got := m.tx.messages[len(m.tx.messages)-1].events
	if len(got) != 1 || got[0].Delta != "queued" {
		t.Fatalf("committed events = %+v, want queued event", got)
	}
}

func TestModel_eventFeedDropsStreamDeltaWhenIdle(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "hi", answer: "final"}))

	nm, _ := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "late"}}})
	m = asModel(t, nm)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "final" {
		t.Errorf("idle event-feed stream delta leaked into the transcript, content = %q", got)
	}
}
