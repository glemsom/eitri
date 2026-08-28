package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

// TestRunEngineTurnReadsLiveSessionKey guards the T1 seam: the engine's
// per-turn SessionKey must read a live mutable key at turn start, not a
// boot-time constant, so a later `/new` can re-key a fresh turn. Each turn
// here re-resolves the key from the mutable holder after it changed.
func TestRunEngineTurnReadsLiveSessionKey(t *testing.T) {
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true, FinishReason: "stop"}), nil
	}), mockTranscript{})

	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir()})
	cfg := config.Default()
	key := tui.NewLiveSessionKey("sess-a")
	turn := runEngineTurn(e, func() config.Config { return cfg }, reg, key, nil, nil, false)

	if _, err := turn(context.Background(), "first", ""); err != nil {
		t.Fatalf("turn error = %v", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("reqs = %d, want 1", len(cap.reqs))
	}
	if got := cap.reqs[0].SessionKey; got != "sess-a" {
		t.Fatalf("turn 1 SessionKey = %q, want %q", got, "sess-a")
	}

	key.Set("sess-b") // `/new` re-mints the session key
	if _, err := turn(context.Background(), "second", ""); err != nil {
		t.Fatalf("turn error = %v", err)
	}
	if len(cap.reqs) != 2 {
		t.Fatalf("reqs = %d, want 2", len(cap.reqs))
	}
	if got := cap.reqs[1].SessionKey; got != "sess-b" {
		t.Fatalf("turn 2 SessionKey = %q, want %q (turn must read the live key)", got, "sess-b")
	}
}
