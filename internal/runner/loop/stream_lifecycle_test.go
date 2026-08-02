package loop

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/runstate"
	"github.com/voocel/litellm"
)

type closeCountingDoneStream struct {
	closed chan struct{}
	count  atomic.Int32
	sent   atomic.Bool
}

func (s *closeCountingDoneStream) Next() (litellm.Event, error) {
	if s.sent.CompareAndSwap(false, true) {
		return litellm.DoneEvent{}, nil
	}
	return nil, io.EOF
}

func (s *closeCountingDoneStream) Close() error {
	s.count.Add(1)
	select {
	case s.closed <- struct{}{}:
	default:
	}
	return nil
}

func TestProcessStream_NormalCompletionStopsCloseWatcher(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	stream := &closeCountingDoneStream{closed: make(chan struct{}, 4)}

	_, _, _, _, err := processStream(ctx, stream, runstate.NewWriter(runstate.New()))
	if err != nil {
		t.Fatalf("processStream returned error: %v", err)
	}

	select {
	case <-stream.closed:
	case <-time.After(time.Second):
		t.Fatal("stream was not closed on normal completion")
	}

	cancel()

	select {
	case <-stream.closed:
		t.Fatalf("stream closed again after processStream returned; close watcher still waited on parent context")
	case <-time.After(100 * time.Millisecond):
	}

	if got := stream.count.Load(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
}
