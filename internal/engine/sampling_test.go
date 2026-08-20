package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type samplingScripted struct {
	provider.Scripted
}

func (s *samplingScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlSamplingPolicy}, nil
}

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

func TestRunSamplingPolicyTemperatureOnSupportingProvider(t *testing.T) {
	t.Parallel()
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

func TestRunSamplingPolicyNucleusOnSupportingProvider(t *testing.T) {
	t.Parallel()
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

func TestRunSamplingPolicyFailsFastWhenUnsupported(t *testing.T) {
	t.Parallel()
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

func TestRunOrdinaryTurnOmitsSamplingPolicy(t *testing.T) {
	t.Parallel()
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
