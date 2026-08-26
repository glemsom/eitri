package app

import (
	"context"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

// blockUntilCancel is a Stream whose Next blocks until the run context is canceled, then
// surfaces the cancellation as the terminating error — the mid-stream shape a live model
// response takes when Ctrl+C/Esc lands while it is still generating.
type blockUntilCancel struct{ ctx context.Context }

func (s *blockUntilCancel) Next() (provider.Chunk, error) {
	<-s.ctx.Done()
	return provider.Chunk{}, s.ctx.Err()
}

// TestRunEngineTurnCancelsStreamedTurn guards the regression where runAgent dropped the
// caller's per-turn context and replaced it with context.Background(), so Ctrl+C while a
// turn streamed never reached the engine (issue #427). The TUI-side stop path
// (TurnSession.Stop -> turnCmd) bottoms out at the tui.Turn seam, which tests there
// fake out; the context-drop lived in internal/app, so this test sits at the runEngineTurn
// boundary where the turn's cancelable context is handed to the shared engine seam.
func TestRunEngineTurnCancelsStreamedTurn(t *testing.T) {
	streamStarted := make(chan struct{})
	e := engine.New(provider.NewScripted(func(ctx context.Context, _ provider.Request) (provider.Stream, error) {
		close(streamStarted)
		return &blockUntilCancel{ctx: ctx}, nil
	}), mockTranscript{})
	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir()})
	cfg := config.Config{Model: "deepseek-v4-flash", ThinkingEnabled: true, ReasoningEffort: "low", CompactionFraction: 0.8}

	turn := runEngineTurn(e, func() config.Config { return cfg }, reg, "sess", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		res tui.TurnResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := turn(ctx, "count to ten slowly", "")
		done <- result{res, err}
	}()

	<-streamStarted // the engine is streaming; stop it mid-run like Ctrl+C does
	cancel()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("turn error = %v, want a stopped result", r.err)
		}
		if !r.res.Stopped {
			t.Fatal("turn not marked stopped after cancel: the per-turn context never reached the engine")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not stop after cancel: the per-turn context was dropped for context.Background() (issue #427)")
	}
}
