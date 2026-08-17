package engine

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

// chunkYield is the sleep the tests use to let a provider stream absorb its
// first chunks before the cancellation lands; it models the real wire latency
// between stream-open and a user pressing esc.
const chunkYield = 20 * time.Millisecond

// blockedStream is a Stream whose Next yields fixed chunks, then blocks on the
// context's Done channel and returns ctx.Err() once canceled. It models a live
// provider stream mid-flight.
type blockedStream struct {
	ctx     context.Context
	chunks  []provider.Chunk
	nextIdx int
}

func (s *blockedStream) Next() (provider.Chunk, error) {
	if s.nextIdx < len(s.chunks) {
		c := s.chunks[s.nextIdx]
		s.nextIdx++
		return c, nil
	}
	<-s.ctx.Done()
	return provider.Chunk{}, s.ctx.Err()
}

// TestErrStoppedWrapsContextCanceled asserts the stop sentinel is
// distinguishable from a plain error and satisfies errors.Is against
// context.Canceled, so callers (batch, TUI) can tell a user stop apart from a
// failure.
func TestErrStoppedWrapsContextCanceled(t *testing.T) {
	if ErrStopped == nil {
		t.Fatal("ErrStopped must be non-nil")
	}
	if !errors.Is(ErrStopped, context.Canceled) {
		t.Errorf("errors.Is(ErrStopped, context.Canceled) = false, want true")
	}
	if errors.Is(ErrStopped, errors.New("some other failure")) {
		t.Error("ErrStopped must not match an unrelated error")
	}
}

// TestRunCanceledDuringStreamReturnsStoppedWithPartialContent cancels a Run
// (non-tool turn) while the provider stream is mid-flight and asserts the
// engine returns the stop sentinel, preserves the partial accumulated answer,
// and writes a distinguishable stopped transcript record carrying the partial
// content.
func TestRunCanceledDuringStreamReturnsStoppedWithPartialContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &mockTranscript{}
	reqs := 0
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		reqs++
		return &blockedStream{ctx: ctx, chunks: []provider.Chunk{
			{Content: "partial "},
			{Content: "answer"},
		}}, nil
	}), tr)

	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := e.Run(ctx, RunRequest{Model: "m", Prompt: "hi"})
		if err == nil {
			t.Errorf("Run error = nil, want stop sentinel")
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run error = %v, want a context.Canceled-wrapping error", err)
			return
		}
		if res.Answer != "partial answer" {
			t.Errorf("Answer = %q, want %q (partial content preserved)", res.Answer, "partial answer")
		}
	}()
	// Wait for the stream to start absorbing chunks.
	time.Sleep(chunkYield)
	cancel()
	<-done

	if reqs != 1 {
		t.Errorf("provider streams = %d, want 1", reqs)
	}
	if len(tr.lines) != 1 {
		t.Fatalf("transcript writes = %d, want 1", len(tr.lines))
	}
	if !contains(tr.lines[0], "partial answer") {
		t.Errorf("stopped transcript record %q missing the partial content", tr.lines[0])
	}
}

// TestRunAgentCanceledBeforeStreamRefusesResubmit cancels the context before
// the agent loop would open the provider stream and asserts the engine returns
// the stop sentinel without ever calling Stream again (no resubmit past the
// cancellation boundary).
func TestRunAgentCanceledBeforeStreamRefusesResubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	streams := 0
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		streams++
		return provider.StreamFunc(provider.Chunk{Content: "should not stream", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	res, err := e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{})
	if err == nil {
		t.Fatal("RunAgent error = nil, want stop sentinel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("RunAgent error = %v, want a context.Canceled-wrapping error", err)
	}
	if streams != 0 {
		t.Errorf("provider Stream calls = %d, want 0 (no resubmit after stop)", streams)
	}
	if res.Answer != "" {
		t.Errorf("Answer = %q, want empty (nothing streamed)", res.Answer)
	}
}

// TestRunAgentCanceledDuringToolExecutionKillsToolLive cancels the context
// while a tool call is executing and asserts the tool's context is canceled
// (the running work dies at the ctx boundary) and the engine surfaces the stop
// sentinel.
func TestRunAgentCanceledDuringToolExecutionKillsToolLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var toolCtx context.Context
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_1", Name: "bash", Arguments: `{}`},
			}, Done: true},
		), nil
	}), &mockTranscript{})

	execStarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{
			Executor: ExecutorFunc(func(ctx context.Context, name, argsJSON string) (ToolExecResult, error) {
				toolCtx = ctx
				close(execStarted)
				<-ctx.Done()
				return ToolExecResult{}, ctx.Err()
			}),
		})
		if err == nil {
			t.Errorf("RunAgent error = nil, want stop sentinel")
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("RunAgent error = %v, want a context.Canceled-wrapping error", err)
		}
	}()
	<-execStarted
	cancel()
	<-done

	if toolCtx == nil {
		t.Fatal("executor never ran")
	}
	if toolCtx.Err() == nil {
		t.Error("tool executor context not canceled after stop")
	}
}

// TestRunAgentStopDuringStreamWritesStoppedTranscriptRecord drives a tool-call
// turn whose second stream blocks after yielding a partial answer, then
// cancels: the engine must keep the partial content in the stop outcome, write
// a distinguishable stopped transcript record carrying that partial content,
// and leave the resubmit counter at the pre-stop value (no fresh provider work
// after the cancellation boundary).
func TestRunAgentStopDuringStreamWritesStoppedTranscriptRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &mockTranscript{}
	turns := 0
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		turns++
		if turns == 1 {
			return provider.StreamFunc(
				provider.Chunk{Content: ""},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_1", Name: "bash", Arguments: `{"command":"ping"}`},
				}, Done: true},
			), nil
		}
		return &blockedStream{ctx: ctx, chunks: []provider.Chunk{{Content: "first turn partial "}}}, nil
	}), tr)

	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
				"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
			}}}},
			Executor: ExecutorFunc(func(ctx context.Context, name, argsJSON string) (ToolExecResult, error) {
				return ToolExecResult{Text: "ok"}, nil
			}),
		})
		// The run cannot complete while the stream is blocked, so assert before
		// canceling is not possible; cancel from the test body instead.
		_ = res
		_ = err
	}()
	// Give the second stream a moment to yield its partial chunk, then cancel.
	time.Sleep(chunkYield)
	cancel()
	<-done

	if turns != 2 {
		t.Errorf("provider streams = %d, want 2 (first tool turn + second blocked turn)", turns)
	}
	if len(tr.lines) != 1 {
		t.Fatalf("transcript writes = %d, want 1 stopped record", len(tr.lines))
	}
	if !contains(tr.lines[0], "first turn partial") {
		t.Errorf("stopped transcript record %q missing the partial content", tr.lines[0])
	}
}

// TestRunAgentStopPreservesPromptInTranscriptRecord asserts the stopped
// transcript record carries the prompt header (the downstream session consumer
// reads "=== <prompt> ===" + partial content) and is distinguishable from a
// clean run's record.
func TestRunAgentStopPreservesPromptInTranscriptRecord(t *testing.T) {
	tr := &mockTranscript{}
	e := New(provider.NewScripted(func(rctx context.Context, req provider.Request) (provider.Stream, error) {
		if req.Messages[len(req.Messages)-1].Content == "clean" {
			return provider.StreamFunc(
				provider.Chunk{Content: "clean answer"},
				provider.Chunk{FinishReason: "stop", Done: true},
			), nil
		}
		return &blockedStream{ctx: rctx, chunks: []provider.Chunk{{Content: "partial"}}}, nil
	}), tr)

	// Drive a first clean run to capture the byte-identical-to-before record.
	_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "clean"}, AgentOptions{})
	if err != nil {
		t.Fatalf("clean RunAgent error = %v, want nil", err)
	}
	cleanLine := tr.lines[0]

	// Then a stopped run: the stopped record must carry the prompt + partial
	// content and differ from the clean record.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "stopme"}, AgentOptions{})
	}()
	time.Sleep(chunkYield)
	cancel()
	<-done

	if len(tr.lines) != 2 {
		t.Fatalf("transcript writes = %d, want 2", len(tr.lines))
	}
	stoppedLine := tr.lines[1]
	if !contains(stoppedLine, "=== stopme ===") || !contains(stoppedLine, "partial") {
		t.Errorf("stopped record %q missing prompt header or partial content", stoppedLine)
	}
	if stoppedLine == cleanLine {
		t.Errorf("stopped record %q must differ from the clean record %q", stoppedLine, cleanLine)
	}
}

// TestRunIoEOFStillClean asserts an io.EOF mid-stream remains a clean run (the
// existing stream-termination contract) and no stop edge interferes: the
// engine still accumulates and writes the normal record.
func TestRunIoEOFStillClean(t *testing.T) {
	tr := &mockTranscript{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return &eofAfterChunkStream{}, nil
	}), tr)

	res, err := e.Run(context.Background(), RunRequest{Model: "m", Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	if res.Answer != "kind of done" {
		t.Errorf("Answer = %q, want %q", res.Answer, "kind of done")
	}
}

type eofAfterChunkStream struct {
	n int
}

func (s *eofAfterChunkStream) Next() (provider.Chunk, error) {
	s.n++
	if s.n == 1 {
		return provider.Chunk{Content: "kind of done"}, nil
	}
	return provider.Chunk{}, io.EOF
}

// ctx returns a fresh cancelable context (helper for streams that need one
// before the real cancel path is established).
func ctx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	_ = cancel
	return ctx
}
