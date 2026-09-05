package tui

import (
	"testing"
	"time"
)

// TestTurnRuntime_DrainReadyBatchesBacklog is the regression lock for the
// per-token render blowup: a real reasoning provider streams one small delta
// per SSE event (reasoning_perdelta_test.go), and each render re-parses the
// live turn's markdown from scratch, so paying one render per delta made cost
// grow quadratically with the turn's size — a long turn's throughput
// collapsed toward the tail. DrainReady must apply every event already queued
// on the feed before the caller renders, so a backlog collapses into one
// render instead of one per delta.
func TestTurnRuntime_DrainReadyBatchesBacklog(t *testing.T) {
	s := NewTurnSession(nil)
	feed := NewEventFeed()
	rt := NewTurnRuntime(s, feed)
	rt.OnTurnStart(0)

	tx := newTestTx()
	tx.busy = true
	tx.messages = append(tx.messages, message{role: "you", content: "prompt"})
	tx.live = s
	txp := &tx

	const n = 50
	for i := 0; i < n; i++ {
		feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "x"}}
	}

	// One event consumed the way the Update loop does, then DrainReady should
	// pick up the rest of the already-queued backlog in the same pass.
	first, ok := feed.TryNext()
	if !ok {
		t.Fatal("expected a queued event")
	}
	if rt.Accept(first) {
		rt.Observe(txp, first)
	}
	rt.DrainReady(txp)

	if got := len(s.flow.Content()); got != n {
		t.Fatalf("expected all %d queued deltas applied by DrainReady, got %d bytes", n, got)
	}
	if _, ok := feed.TryNext(); ok {
		t.Fatal("expected the feed to be fully drained")
	}
}

// TestEventFeed_TryNext exercises the non-blocking read directly: empty on an
// empty feed, returns queued events in order, and reports empty again once
// drained.
func TestEventFeed_TryNext(t *testing.T) {
	feed := NewEventFeed()
	if _, ok := feed.TryNext(); ok {
		t.Fatal("expected empty feed to report no event")
	}
	feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "a"}}
	feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "b"}}
	u1, ok := feed.TryNext()
	if !ok || u1.Stream.Delta != "a" {
		t.Fatalf("expected first queued event %q, got %+v ok=%v", "a", u1, ok)
	}
	u2, ok := feed.TryNext()
	if !ok || u2.Stream.Delta != "b" {
		t.Fatalf("expected second queued event %q, got %+v ok=%v", "b", u2, ok)
	}
	if _, ok := feed.TryNext(); ok {
		t.Fatal("expected feed to report empty after draining")
	}
}

// TestQuadraticLiveTail_StaysBounded is a tight, deterministic timing
// regression: it streams many small reasoning deltas through the live feed
// the way the real Update loop does (one blocking receive, then
// DrainReady, then one render) and asserts wall time stays far below the
// pre-fix quadratic cost. Before the fix, streaming this many deltas of
// growing markdown took on the order of a minute; the batch-drain keeps it
// under a second.
func TestQuadraticLiveTail_StaysBounded(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := newTestTx()
	tx.busy = true
	tx.messages = append(tx.messages, message{role: "you", content: "live prompt"})
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: true})

	s := NewTurnSession(nil)
	tx.live = s
	txp := &tx
	feed := NewEventFeed()
	rt := NewTurnRuntime(s, feed)
	rt.OnTurnStart(0)

	chunk := "chain of thought reasoning tokens and analysis segment number "
	const totalDeltas = 2000
	wantLen := totalDeltas * (len(chunk) + 1)

	go func() {
		for i := 0; i < totalDeltas; i++ {
			feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: chunk + string(rune('a'+i%26))}}
		}
	}()

	start := time.Now()
	for len(s.flow.Reasoning()) < wantLen {
		u, ok := <-feed.updates
		if !ok {
			break
		}
		if rt.Accept(u) {
			rt.Observe(txp, u)
		}
		rt.DrainReady(txp)
		_ = txp.renderPaneContent()
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("streaming %d deltas took %v, want well under the pre-fix ~76s", totalDeltas, elapsed)
	}
}
