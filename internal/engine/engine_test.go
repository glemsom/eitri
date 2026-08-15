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

// capturedRequests records every provider.Request the engine issues, so a test
// can assert the reasoning-control head (thinking + normalized effort) is
// threaded through the engine seam.
type capturedRequests struct {
	reqs []provider.Request
}

// TestRunThreadsThinkingAndEffort verifies the engine passes the reasoning
// controls through to the provider: thinking enabled and the configured
// reasoning_effort are set on every outgoing Request.
func TestRunThreadsThinkingAndEffort(t *testing.T) {
	cap := &capturedRequests{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	_, err := e.Run(context.Background(), RunRequest{
		Model:           "deepseek-v4-flash",
		Prompt:          "go",
		ThinkingEnabled: true,
		ReasoningEffort: "medium", // normalized to high by the provider
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	if !cap.reqs[0].ThinkingEnabled {
		t.Error("engine did not set ThinkingEnabled on the request")
	}
	if cap.reqs[0].ReasoningEffort != "medium" {
		t.Errorf("engine ReasoningEffort = %q, want %q", cap.reqs[0].ReasoningEffort, "medium")
	}
}

// TestRunAgentPersistsReasoningOnToolTurns drives a reasoning-then-tool-call
// fixture through the engine: turn one streams real reasoning and a tool call;
// turn two streams real reasoning and the final answer. The engine must keep
// the reasoning on the assistant message it re-emits for the tool turn (so the
// resubmitted history never strips it, tripping DeepSeek's 400) and surface
// the final turn's reasoning on the result.
func TestRunAgentPersistsReasoningOnToolTurns(t *testing.T) {
	assistantTurn := 0
	var assistantReasons []string // reasoning_content the engine re-emits per assistant turn
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		// Collect the reasoning_content of every assistant message the engine
		// carries forward into a later request.
		for _, m := range req.Messages {
			if m.Role == provider.RoleAssistant {
				assistantReasons = append(assistantReasons, m.ReasoningContent)
			}
		}
		switch assistantTurn {
		case 0:
			assistantTurn++
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "turn-one reasoning"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_a", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		default:
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "turn-two reasoning"},
				provider.Chunk{Content: "final answer", FinishReason: "stop", Done: true},
			), nil
		}
	})

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
		}}}},
		Executor: &mockToolRecorder{},
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "final answer" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "final answer")
	}
	if res.Reasoning != "turn-two reasoning" {
		t.Fatalf("Result.Reasoning = %q, want the final turn's reasoning", res.Reasoning)
	}
	// The resubmitted history must carry the first turn's real reasoning on its
	// assistant message (the tool-call turn), so the provider never 400s.
	found := false
	for _, r := range assistantReasons {
		if r == "turn-one reasoning" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("assistant reasoning re-emitted = %v, want turn-one reasoning preserved on the tool turn", assistantReasons)
	}
}

// capableScripted is a Provider that both scripts turns and declares a fixed set
// of supported generation controls, exercising the engine's negotiation seam
// (issue #58).
type capableScripted struct {
	*provider.Scripted
	supported []provider.GenerationControl
}

// SupportedGenerationControls implements provider.GenerationControlProvider.
func (c *capableScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return append([]provider.GenerationControl(nil), c.supported...), nil
}

// TestEngineNegotiatesGenerationControls verifies the engine forwards a special
// turn's generation-control requirements to the provider capability surface,
// returning the honored controls and failing a required control the provider
// cannot honor (before any wire call).
func TestEngineNegotiatesGenerationControls(t *testing.T) {
	p := &capableScripted{
		Scripted:  provider.NewScripted(nil),
		supported: []provider.GenerationControl{provider.GenerationControlGenerationBudget},
	}
	e := New(p, &mockTranscript{})

	// An unsupported required control fails negotiation before any stream.
	_, err := e.NegotiateGenerationControls(context.Background(), []provider.ControlRequirement{{
		Control:  provider.GenerationControlJSONObjectMode,
		Required: true,
	}})
	if err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want unsupported-required error")
	}

	// A supported optional control is honored and returned.
	got, err := e.NegotiateGenerationControls(context.Background(), []provider.ControlRequirement{{
		Control:  provider.GenerationControlGenerationBudget,
		Required: false,
	}})
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != provider.GenerationControlGenerationBudget {
		t.Fatalf("NegotiateGenerationControls() = %v, want [generation_budget]", got)
	}
}
