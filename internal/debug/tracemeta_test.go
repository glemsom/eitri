package debug

import (
	"testing"
	"time"
)

func TestTraceMeta_RunTurnCorrelation(t *testing.T) {
	meta := &TraceMeta{}
	meta.SetRunID("run-1")
	meta.SetTurn(3)

	if meta.RunID() != "run-1" {
		t.Errorf("RunID() = %q, want %q", meta.RunID(), "run-1")
	}
	if meta.Turn() != 3 {
		t.Errorf("Turn() = %d, want 3", meta.Turn())
	}
}

func TestTraceMeta_TimeToFirstToken(t *testing.T) {
	meta := &TraceMeta{}
	start := time.Now()
	meta.SetRequestStart(start)
	meta.SetFirstTokenTime(start.Add(250 * time.Millisecond))

	if got := meta.TTFTMs(); got != 250 {
		t.Fatalf("TTFTMs() = %d, want 250", got)
	}
	if got := meta.FirstTokenTime(); got.IsZero() {
		t.Fatal("FirstTokenTime() = zero, want recorded time")
	}

	// Only the first token time is kept: later tokens must not shift TTFT.
	meta.SetFirstTokenTime(start.Add(900 * time.Millisecond))
	if got := meta.TTFTMs(); got != 250 {
		t.Fatalf("TTFTMs() after later token = %d, want 250 (first token only)", got)
	}
}

func TestTraceMeta_TTFTRequiresBothEndpoints(t *testing.T) {
	// No request start → 0.
	meta := &TraceMeta{}
	meta.SetFirstTokenTime(time.Now())
	if got := meta.TTFTMs(); got != 0 {
		t.Fatalf("TTFTMs() without request start = %d, want 0", got)
	}

	// No first token → 0.
	meta2 := &TraceMeta{}
	meta2.SetRequestStart(time.Now())
	if got := meta2.TTFTMs(); got != 0 {
		t.Fatalf("TTFTMs() without first token = %d, want 0", got)
	}
}

func TestTraceMeta_NilReceiverSafe(t *testing.T) {
	var meta *TraceMeta
	meta.SetRunID("run-1")
	meta.SetTurn(1)
	meta.SetAttempt(2)
	meta.SetRequestStart(time.Now())
	meta.SetFirstTokenTime(time.Now())

	if meta.RunID() != "" {
		t.Errorf("nil RunID() = %q, want empty", meta.RunID())
	}
	if meta.Turn() != 0 {
		t.Errorf("nil Turn() = %d, want 0", meta.Turn())
	}
	if meta.Attempt() != 0 {
		t.Errorf("nil Attempt() = %d, want 0", meta.Attempt())
	}
	if !meta.RequestStart().IsZero() || !meta.FirstTokenTime().IsZero() {
		t.Error("nil receiver should expose zero times")
	}
	if meta.TTFTMs() != 0 {
		t.Errorf("nil TTFTMs() = %d, want 0", meta.TTFTMs())
	}
}
