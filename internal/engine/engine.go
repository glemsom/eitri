// Package engine drives a single agent run turn over the provider seam. It is
// the shared engine behind both the TUI and batch mode; every code path that
// talks to a model goes through here. This ticket (T1c) implements the
// non-tool turn: send model + messages, stream deltas, produce the final
// assistant answer, and record the run in the transcript.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/glemsom/eitri/internal/provider"
)

// ErrMaxTurns is returned when a tool-call loop exceeds the configured cap.
// It bounds runaway agent loops (docs/spec.md §2.1, eitri.md §2.1).
var ErrMaxTurns = errors.New("maximum turn limit reached")

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

// ToolExecutor executes an agent tool call. The tools registry implements it;
// the engine depends on this seam so dispatch is testable without filesystem
// side effects a specific tool might have.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, argsJSON string) (string, error)
}

// ExecutorFunc adapts a plain function to the ToolExecutor interface.
type ExecutorFunc func(ctx context.Context, name string, argsJSON string) (string, error)

// Execute implements ToolExecutor.
func (f ExecutorFunc) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	return f(ctx, name, argsJSON)
}

// AgentOptions configures the tool-call dispatch loop (docs/spec.md §2).
// Tools and ToolChoice are the request-head tool definitions sent to the
// provider (kept stable per session for prompt caching); Executor runs calls;
// MaxTurns caps the loop (0 = uncapped).
type AgentOptions struct {
	Tools      []provider.Tool
	ToolChoice any
	Executor   ToolExecutor
	MaxTurns   int
}

// RunAgent drives a tool-capable agent run: it maintains one mutable messages
// list, executes any returned tool_calls (single-call path is the floor here;
// hardening is T5), appends a matching role:"tool" result per call, and
// resubmits until the model stops calling tools. Result.Answer/Reasoning/Usage
// reflect the final, tool-free turn.
func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (Result, error) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}}
	var final Result

	for turn := 0; ; turn++ {
		if opts.MaxTurns > 0 && turn >= opts.MaxTurns {
			return final, ErrMaxTurns
		}
		s, err := e.provider.Stream(ctx, provider.Request{
			Model:      req.Model,
			Messages:   messages,
			Tools:      opts.Tools,
			ToolChoice: opts.ToolChoice,
		})
		if err != nil {
			return final, err
		}

		var content, reasoning string
		var done provider.Chunk
		for {
			c, err := s.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return final, err
			}
			content += c.Content
			reasoning += c.ReasoningContent
			if c.Usage != nil {
				final.Usage = c.Usage
			}
			done = c
			if c.Done {
				break
			}
		}

		assistant := provider.Message{
			Role: provider.RoleAssistant,
			// DeepSeek requires assistant messages to carry reasoning_content
			// (empty-ok) and real reasoning to persist on tool turns (spec §6).
			Content:          content,
			ReasoningContent: reasoning,
		}

		// No tool calls on the terminal chunk: this turn is the final answer.
		if len(done.ToolCalls) == 0 {
			final.Answer = content
			final.Reasoning = reasoning
			if e.transcript != nil {
				_ = e.transcript.WriteTranscript([]byte(fmt.Sprintf("=== %s ===\n%s\n", req.Prompt, content)))
			}
			return final, nil
		}

		assistant.ToolCalls = done.ToolCalls
		messages = append(messages, assistant)
		for _, tc := range done.ToolCalls {
			result, err := opts.Executor.Execute(ctx, tc.Name, tc.Arguments)
			if err != nil {
				result = "error executing tool: " + err.Error()
			}
			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}
}
func closeErr(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
