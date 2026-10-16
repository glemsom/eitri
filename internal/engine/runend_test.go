package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type runEndSinkFunc func(reason string)

func (f runEndSinkFunc) LogRunEnd(reason string) { f(reason) }

func collectRunEnds(e *Engine) *[]string {
	reasons := &[]string{}
	e.SetRunEndSink(runEndSinkFunc(func(reason string) { *reasons = append(*reasons, reason) }))
	return reasons
}

func transcriptHas(lines []string, sub string) bool {
	for _, l := range lines {
		if contains(l, sub) {
			return true
		}
	}
	return false
}

func TestRunAgentMaxTurnsRecordsReason(t *testing.T) {
	t.Parallel()
	tr := &mockTranscript{}
	e := New(toolForever(100), tr)
	reasons := collectRunEnds(e)

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "loop"}, AgentOptions{
		Tools:       []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		Executor:    &mockToolRecorder{},
		MaxTurns:    2,
		CanContinue: nil,
	})
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("RunAgent() error = %v, want ErrMaxTurns", err)
	}
	if len(*reasons) != 1 || (*reasons)[0] != provider.RunEndMaxTurns {
		t.Fatalf("run-end reasons = %v, want [%s]", *reasons, provider.RunEndMaxTurns)
	}
	if !transcriptHas(tr.lines, "[max_turns]") {
		t.Fatalf("transcript %v does not name the turn cap", tr.lines)
	}
}

func TestRunAgentStoppedNamesTheStop(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &mockTranscript{}
	e := New(toolForever(100), tr)
	reasons := collectRunEnds(e)

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.RunAgent(ctx, RunRequest{Model: "m", Prompt: "loop"}, AgentOptions{
			Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor: &mockToolRecorder{},
			MaxTurns: 1,
			CanContinue: func() bool {
				close(entered)
				<-release
				return false
			},
		})
	}()
	<-entered
	cancel()
	close(release)
	<-done

	if len(*reasons) != 1 || (*reasons)[0] != provider.RunEndStopped {
		t.Fatalf("run-end reasons = %v, want [%s]", *reasons, provider.RunEndStopped)
	}
	if !transcriptHas(tr.lines, "run ended by user stop") {
		t.Fatalf("transcript %v does not name the stop", tr.lines)
	}
}

func TestRunAgentCompletedRecordsReason(t *testing.T) {
	t.Parallel()
	e := New(provider.NewFake("../provider/testdata/hello.sse"), &mockTranscript{})
	reasons := collectRunEnds(e)

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{}); err != nil {
		t.Fatalf("run error = %v", err)
	}
	if len(*reasons) != 1 || (*reasons)[0] != provider.RunEndCompleted {
		t.Fatalf("run-end reasons = %v, want [%s]", *reasons, provider.RunEndCompleted)
	}
}

// failingTranscript fails every write, standing in for a full or unwritable disk.
type failingTranscript struct {
	writes int
}

func (f *failingTranscript) WriteTranscript([]byte) error {
	f.writes++
	return errors.New("disk full")
}

func TestRunAgentReportsTranscriptFailuresOnceAtRunEnd(t *testing.T) {
	t.Parallel()
	ft := &failingTranscript{}
	e := New(provider.NewFake("../provider/testdata/hello.sse"), ft)
	var reported []int
	e.SetTranscriptErrorReporter(func(failed int) { reported = append(reported, failed) })

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi"}, AgentOptions{}); err != nil {
		t.Fatalf("run error = %v", err)
	}
	if ft.writes == 0 {
		t.Fatal("transcript writer never invoked")
	}
	if len(reported) != 1 {
		t.Fatalf("error reporter calls = %v, want exactly one", reported)
	}
	if reported[0] != ft.writes {
		t.Fatalf("reported failures = %d, want %d", reported[0], ft.writes)
	}
}
