package provider

import (
	"context"
	"errors"
	"io"
)

func StreamFunc(chunks ...Chunk) Stream {
	return &sliceStream{chunks: chunks}
}

type sliceStream struct {
	chunks []Chunk
	idx    int
}

func (s *sliceStream) Next() (Chunk, error) {
	if s.idx >= len(s.chunks) {
		return Chunk{}, io.EOF
	}
	c := s.chunks[s.idx]
	s.idx++
	return c, nil
}

type Handler func(ctx context.Context, req Request) (Stream, error)

// Scripted is a deterministic Provider driven by a Handler, letting engine and dispatch tests script exact tool-call turns without SSE fixtures.
type Scripted struct {
	h Handler
}

func NewScripted(h Handler) *Scripted {
	return &Scripted{h: h}
}

func (sp *Scripted) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return []GenerationControl{GenerationControlGenerationBudget}, nil
}

func (sp *Scripted) Stream(ctx context.Context, req Request) (Stream, error) {
	if sp.h == nil {
		return nil, errors.New("scripted provider: nil handler")
	}
	return sp.h(ctx, req)
}
