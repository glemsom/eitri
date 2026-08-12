// Package engine drives a single agent run turn over the provider seam. It is
// the shared engine behind both the TUI and batch mode; every code path that
// talks to a model goes through here. This ticket (T1c) implements the
// non-tool turn: send model + messages, stream deltas, produce the final
// assistant answer, and record the run in the transcript.
package engine

import (
	"context"
	"fmt"
	"io"

	"github.com/glemsom/eitri/internal/provider"
)

// TranscriptWriter records the run's on-disk trail (the T1b session sink).
type TranscriptWriter interface {
	WriteTranscript(line []byte) error
}

// Engine is a run engine bound to a provider and a transcript sink.
type Engine struct {
	provider   provider.Provider
	transcript TranscriptWriter
}

// New returns an Engine that talks to p and appends run records to tr.
func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr}
}

// RunRequest is a single non-tool turn of work.
type RunRequest struct {
	Model  string
	Prompt string
}

// Result is the outcome of one Run.
type Result struct {
	Answer    string
	Reasoning string
	Usage     *provider.Usage
}

// Run performs a non-tool turn: it sends the model + a user message and streams
// the provider response to a final assistant answer. Thinking is surfaced on a
// separate channel (never merged into the answer) and the run is recorded on
// the transcript sink.
func (e *Engine) Run(ctx context.Context, req RunRequest) (Result, error) {
	s, err := e.provider.Stream(ctx, provider.Request{
		Model:    req.Model,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}},
	})
	if err != nil {
		return Result{}, err
	}

	var res Result
	for {
		c, err := s.Next()
		if err != nil {
			return res, closeErr(err)
		}
		res.Answer += c.Content
		res.Reasoning += c.ReasoningContent
		if c.Usage != nil {
			res.Usage = c.Usage
		}
		if c.Done {
			break
		}
	}

	if e.transcript != nil {
		_ = e.transcript.WriteTranscript([]byte(fmt.Sprintf("=== %s ===\n%s\n", req.Prompt, res.Answer)))
	}
	return res, nil
}

// closeErr maps io.EOF on Next to a clean done; any other error propagates.
func closeErr(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
