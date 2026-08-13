package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// samplingScripted wraps a Scripted handler so it also declares, via the
// generation-control capability surface, that it honors the Sampling Policy
// control (docs/spec.md §13 / issue #61). The engine opts a sampling-policy
// special turn into that control, so the turn's request must carry the policy.
type samplingScripted struct {
	provider.Scripted
}

// SupportedGenerationControls implements provider.GenerationControlProvider.
func (s *samplingScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlSamplingPolicy}, nil
}

// samplingHandler records every request it serves and returns a fixed answer so
// a test can assert the path end to end.
type samplingHandler struct {
	requests []provider.Request
}

func (s *samplingHandler) stream(_ context.Context, req provider.Request) (provider.Stream, error) {
	s.requests = append(s.requests, req)
	return provider.StreamFunc(
		provider.Chunk{Content: "sampled answer"},
		provider.Chunk{FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 5, CompletionTokens: 3}},
	), nil
}

// TestRunSamplingPolicyTemperatureOnSupportingProvider verifies the
// temperature-based Sampling Policy special turn (docs/spec.md §13 / issue #61):
// on a provider that honors the control, the engine issues a non-tool turn
// carrying a temperature sampling policy — so the wire emits temperature and
// never top_p — and returns the generated answer. The session key and prompt
// thread through unchanged.
func TestRunSamplingPolicyTemperatureOnSupportingProvider(t *testing.T) {
	h := &samplingHandler{}
	e := New(&samplingScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	res, err := e.RunSamplingPolicy(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "Sample a short continuation",
		SessionKey: "sess-sample",
	}, provider.SamplingPolicy{Mode: provider.SamplingTemperature, Value: 0.82})
	if err != nil {
		t.Fatalf("RunSamplingPolicy() error = %v, want nil", err)
	}
	if res.Answer != "sampled answer" {
		t.Fatalf("Answer = %q, want the sampled answer", res.Answer)
	}
	if res.Usage == nil || res.Usage.CompletionTokens != 3 {
		t.Fatalf("Usage = %+v, want completion tokens 3 threaded through", res.Usage)
	}
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want exactly 1 sampling turn", len(h.requests))
	}
	if h.requests[0].Sampling == nil {
		t.Fatal("sampling turn request Sampling = nil, want the requested policy")
	}
	if h.requests[0].Sampling.Mode != provider.SamplingTemperature || h.requests[0].Sampling.Value != 0.82 {
		t.Fatalf("sampling request policy = %+v, want temperature 0.82", h.requests[0].Sampling)
	}
	if len(h.requests[0].Tools) != 0 {
		t.Fatalf("sampling turn request carried tools, want a non-tool special turn")
	}
	if got := h.requests[0].SessionKey; got != "sess-sample" {
		t.Fatalf("sampling request SessionKey = %q, want sess-sample", got)
	}
}

// TestRunSamplingPolicyNucleusOnSupportingProvider verifies the nucleus
// (top-p) sampling form of the special turn (issue #61) carries the nucleus
// policy so the wire emits top_p and never temperature.
func TestRunSamplingPolicyNucleusOnSupportingProvider(t *testing.T) {
	h := &samplingHandler{}
	e := New(&samplingScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	_, err := e.RunSamplingPolicy(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "Sample a nucleus continuation",
	}, provider.SamplingPolicy{Mode: provider.SamplingNucleus, Value: 0.95})
	if err != nil {
		t.Fatalf("RunSamplingPolicy() error = %v, want nil", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want exactly 1 sampling turn", len(h.requests))
	}
	if h.requests[0].Sampling == nil || h.requests[0].Sampling.Mode != provider.SamplingNucleus || h.requests[0].Sampling.Value != 0.95 {
		t.Fatalf("sampling request policy = %+v, want nucleus 0.95", h.requests[0].Sampling)
	}
}

// TestRunSamplingPolicyFailsFastWhenUnsupported verifies the generation-control
// contract (docs/spec.md §13 / issue #61): a provider without the Sampling
// Policy capability honors no controls, so a required sampling-policy special
// turn fails negotiation fast — before any wire call.
func TestRunSamplingPolicyFailsFastWhenUnsupported(t *testing.T) {
	// NewScripted has no generation-control capability surface: it honors no
	// controls, so a required Sampling Policy fails the contract.
	h := &samplingHandler{}
	e := New(provider.NewScripted(h.stream), &mockTranscript{})

	_, err := e.RunSamplingPolicy(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "Sample a constrained continuation",
	}, provider.SamplingPolicy{Mode: provider.SamplingTemperature, Value: 0.82})
	var unsupported *provider.UnsupportedRequiredControlError
	if !errors.As(err, &unsupported) {
		t.Fatalf("RunSamplingPolicy() error = %v, want *provider.UnsupportedRequiredControlError", err)
	}
	if unsupported.Control != provider.GenerationControlSamplingPolicy {
		t.Fatalf("unsupported control = %q, want sampling_policy", unsupported.Control)
	}
	if len(h.requests) != 0 {
		t.Fatalf("provider requests = %d, want 0 — the turn must fail before any wire call", len(h.requests))
	}
}

// TestRunOrdinaryTurnOmitsSamplingPolicy verifies that an ordinary agent/tool
// turn never carries a sampling policy, so the byte-identical request head is
// preserved for the prompt cache (docs/spec.md §4 / issue #61): the sampling
// seam is special-turn only.
func TestRunOrdinaryTurnOmitsSamplingPolicy(t *testing.T) {
	h := &samplingHandler{}
	e := New(&samplingScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	_, err := e.Run(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "ordinary turn",
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want exactly 1 turn", len(h.requests))
	}
	if h.requests[0].Sampling != nil {
		t.Fatalf("ordinary request Sampling = %+v, want nil (special-turn only)", h.requests[0].Sampling)
	}
}
