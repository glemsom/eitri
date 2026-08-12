package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
)

// Fake is a deterministic fake Chat-Completions provider that reads a committed
// fixture file and streams it back as text/event-stream chunks — no network,
// fully reproducible. It is the fixture-spawning side of the engine test seam
// (docs/spec.md, "Primary test seam: the run engine, via a fake LLM").
type Fake struct {
	path string
}

// NewFake returns a fake provider that replays the SSE fixture at path.
func NewFake(path string) *Fake {
	return &Fake{path: path}
}

// Stream implements Provider by replaying the fixture. It ignores the request
// body; the fixture is the source of truth for behaviour.
func (f *Fake) Stream(_ context.Context, _ Request) (Stream, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	return &fakeStream{ev: newSSE(bytes.NewReader(data)), acc: newToolAccumulator()}, nil
}

// fakeStream adapts the parsed SSE events into the Stream seam, accumulating
// streamed tool_call fragments across the turn.
type fakeStream struct {
	ev  *sse
	acc *toolAccumulator
}

// Next implements Stream.
func (fs *fakeStream) Next() (Chunk, error) {
	e, err := fs.ev.Next()
	if errors.Is(err, io.EOF) {
		return Chunk{}, io.EOF
	}
	if err != nil {
		return Chunk{}, err
	}
	return parseEvent(e.data, fs.acc)
}
