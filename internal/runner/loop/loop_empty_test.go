package loop

import (
	"context"
	"testing"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/runstate"
)

// emptyStreamProvider simulates a provider that returns a stream with
// a DoneEvent immediately (no content, no tool calls).
type emptyStreamProvider struct{}

func (p *emptyStreamProvider) Name() string { return "empty-stream" }

func (p *emptyStreamProvider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return &litellm.Response{}, nil
}

func (p *emptyStreamProvider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	return &doneImmediatelyStream{}, nil
}

type doneImmediatelyStream struct{}

func (s *doneImmediatelyStream) Next() (litellm.Event, error) {
	return litellm.DoneEvent{}, nil
}

func (s *doneImmediatelyStream) Close() error { return nil }

func TestRunAgent_EmptyStream_CompletesSuccessfully(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client, err := litellm.New(&emptyStreamProvider{})
	if err != nil {
		t.Fatalf("litellm.New: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "use a subagent"}}},
		},
		lrWithModel("test-model"),
	)

	err = RunAgent(context.Background(), RunSpec{
		Client:    client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr: NewRequestHistoryManager(req),
		Confirmer:  nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify done event was sent
	events := collectSSE(sseState)
	foundDone := false
	for _, evt := range events {
		if evt.Type == "done" {
			foundDone = true
			break
		}
	}
	if !foundDone {
		t.Errorf("expected done event")
	}

	// Verify no token events (content was empty)
	for _, evt := range events {
		if evt.Type == "token" {
			t.Errorf("unexpected token event for empty stream, got: %+v", evt)
			}
	}

	// Verify NO empty assistant message was appended — empty assistant
	// messages produce invalid {"role":"assistant"} without content
	// which some providers reject.
	if len(req.Messages) != 1 {
		t.Fatalf("req.Messages length = %d, want 1 (only the original user message; empty assistant should not be appended)", len(req.Messages))
	}
}
