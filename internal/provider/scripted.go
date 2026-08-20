package provider

import (
	"context"
	"errors"
	"io"
)

// StreamFunc builds a Stream that yields a fixed sequence of chunks and then io.EOF.
func StreamFunc(chunks ...Chunk) Stream {
	return &sliceStream{chunks: chunks}
}

// sliceStream replays a pre-built chunk slice.
type sliceStream struct {
	chunks []Chunk
	idx    int
}

// Next implements Stream.
func (s *sliceStream) Next() (Chunk, error) {
	if s.idx >= len(s.chunks) {
		return Chunk{}, io.EOF
	}
	c := s.chunks[s.idx]
	s.idx++
	return c, nil
}

// Handler decides how a Scripted provider responds to one request.
type Handler func(ctx context.Context, req Request) (Stream, error)

// Scripted is a deterministic Provider driven by a Handler, letting engine and dispatch tests script exact tool-call turns without SSE fixtures.
type Scripted struct {
	h Handler
}

// NewScripted returns a Provider whose turns are produced by h.
func NewScripted(h Handler) *Scripted {
	return &Scripted{h: h}
}

// Stream implements Provider by delegating to the handler.
func (sp *Scripted) Stream(ctx context.Context, req Request) (Stream, error) {
	if sp.h == nil {
		return nil, errors.New("scripted provider: nil handler")
	}
	return sp.h(ctx, req)
}
