package engine

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/testutil"
)

type blockedStream struct {
	ctx     context.Context
	chunks  []provider.Chunk
	nextIdx int
	ready   chan struct{}
}

func (s *blockedStream) Next() (provider.Chunk, error) {
	if s.ready != nil && s.nextIdx == 0 {
		close(s.ready)
	}
	if s.nextIdx < len(s.chunks) {
		c := s.chunks[s.nextIdx]
		s.nextIdx++
		return c, nil
	}
	<-s.ctx.Done()
	return provider.Chunk{}, s.ctx.Err()
}

type eofAfterCancelStream struct {
	ctx     context.Context
	chunks  []provider.Chunk
	nextIdx int
	ready   chan struct{}
}

func (s *eofAfterCancelStream) Next() (provider.Chunk, error) {
	if s.nextIdx < len(s.chunks) {
		c := s.chunks[s.nextIdx]
		s.nextIdx++
		if s.nextIdx == len(s.chunks) && s.ready != nil {
			close(s.ready)
		}
		return c, nil
	}
	<-s.ctx.Done()
	return provider.Chunk{}, io.EOF
}

func TestErrStoppedWrapsContextCanceled(t *testing.T) {
	t.Parallel()
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

func TestRunAgentCanceledBeforeStreamRefusesResubmit(t *testing.T) {
	t.Parallel()
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

func TestRunAgentCanceledDuringToolExecutionKillsToolLive(t *testing.T) {
	t.Parallel()
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
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
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

func TestRunAgentStopDuringStreamWritesStoppedTranscriptRecord(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	tr := &mockTranscript{}
	turns := 0
	started := make(chan struct{})
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
		return &blockedStream{ctx: ctx, ready: started, chunks: []provider.Chunk{{Content: "first turn partial "}}}, nil
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
		_ = res
		_ = err
	}()
	testutil.Await(t, "second provider stream to start", started)
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

func TestRunAgentCanceledStreamEOFIsStopped(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	tr := &mockTranscript{}
	started := make(chan struct{})
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return &eofAfterCancelStream{ctx: ctx, ready: started, chunks: []provider.Chunk{{Content: "partial"}}}, nil
	}), tr)

	done := make(chan struct{})
	var res Result
	var err error
	go func() {
		defer close(done)
		res, err = e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "stopme"}, AgentOptions{})
	}()
	testutil.Await(t, "provider stream to emit partial", started)
	cancel()
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunAgent error = %v, want context.Canceled-wrapping stop", err)
	}
	if res.Answer != "partial" {
		t.Fatalf("Answer = %q, want stopped partial", res.Answer)
	}
	if len(tr.lines) != 1 || !contains(tr.lines[0], "[stopped]") {
		t.Fatalf("transcript writes = %v, want one stopped record", tr.lines)
	}
}

func TestRunAgentStopPreservesPromptInTranscriptRecord(t *testing.T) {
	t.Parallel()
	tr := &mockTranscript{}
	started := make(chan struct{})
	e := New(provider.NewScripted(func(rctx context.Context, req provider.Request) (provider.Stream, error) {
		if req.Messages[len(req.Messages)-1].Content == "clean" {
			return provider.StreamFunc(
				provider.Chunk{Content: "clean answer"},
				provider.Chunk{FinishReason: "stop", Done: true},
			), nil
		}
		return &blockedStream{ctx: rctx, ready: started, chunks: []provider.Chunk{{Content: "partial"}}}, nil
	}), tr)

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "clean"}, AgentOptions{})
	if err != nil {
		t.Fatalf("clean RunAgent error = %v, want nil", err)
	}
	cleanLine := tr.lines[0]

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "stopme"}, AgentOptions{})
	}()
	testutil.Await(t, "stopped provider stream to start", started)
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
