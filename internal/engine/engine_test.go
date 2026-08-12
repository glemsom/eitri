package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// mockTranscript records transcript writes so we can assert at the seam.
type mockTranscript struct {
	lines []string
}

func (m *mockTranscript) WriteTranscript(line []byte) error {
	m.lines = append(m.lines, string(line))
	return nil
}

// TestRunProducesFinalAnswer drives the engine end-to-end through the fake
// provider seam for a non-tool turn and asserts the final assistant answer,
// reasoning channel, usage, and the transcript write.
func TestRunProducesFinalAnswer(t *testing.T) {
	tr := &mockTranscript{}
	e := New(provider.NewFake("../provider/testdata/hello.sse"), tr)

	res, err := e.Run(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "Say hello",
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if res.Answer != "Hello world" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "Hello world")
	}
	if res.Reasoning != "think step by step" {
		t.Fatalf("Reasoning = %q, want %q", res.Reasoning, "think step by step")
	}
	if res.Usage == nil || res.Usage.PromptTokens != 12 {
		t.Fatalf("Usage = %+v, want prompt=12", res.Usage)
	}
	if len(tr.lines) == 0 {
		t.Fatal("transcript never written")
	}
}

// TestRunWritesAnswerToTranscript verifies the final answer lands in the
// transcript via the T1b trace/sink seam.
func TestRunWritesAnswerToTranscript(t *testing.T) {
	tr := &mockTranscript{}
	e := New(provider.NewFake("../provider/testdata/usage-final.sse"), tr)

	res, err := e.Run(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if res.Answer != "ack" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "ack")
	}
	found := false
	for _, l := range tr.lines {
		if contains(l, "ack") {
			found = true
		}
	}
	if !found {
		t.Fatalf("transcript lines %v do not contain the answer", tr.lines)
	}
}

// contains reports whether s contains the substring sub.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
