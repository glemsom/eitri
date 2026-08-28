package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

func TestRunEngineTurnReadsCurrentConfig(t *testing.T) {
	var reqs []provider.Request
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		reqs = append(reqs, req)
		return provider.StreamFunc(
			provider.Chunk{Content: "ok"},
			provider.Chunk{Done: true, FinishReason: "stop"},
		), nil
	}), mockTranscript{})
	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir()})
	cfg := config.Config{Model: "first", ThinkingEnabled: true, ReasoningEffort: "low", CompactionFraction: 0.8}

	turn := runEngineTurn(e, func() config.Config { return cfg }, reg, tui.NewLiveSessionKey("sess-"+t.Name()), nil, nil, false)
	if _, err := turn(context.Background(), "one", ""); err != nil {
		t.Fatalf("first turn error = %v, want nil", err)
	}
	cfg.Model = "second"
	cfg.ThinkingEnabled = false
	cfg.ReasoningEffort = "max"
	if _, err := turn(context.Background(), "two", ""); err != nil {
		t.Fatalf("second turn error = %v, want nil", err)
	}

	if len(reqs) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(reqs))
	}
	if reqs[0].Model != "first" {
		t.Fatalf("first turn model = %q, want first", reqs[0].Model)
	}
	if reqs[1].Model != "second" {
		t.Fatalf("second turn model = %q, want second", reqs[1].Model)
	}
	if reqs[1].ThinkingEnabled {
		t.Fatal("second turn ThinkingEnabled = true, want false from updated config")
	}
	if reqs[1].ReasoningEffort != "" {
		t.Fatalf("second turn ReasoningEffort = %q, want empty when thinking disabled", reqs[1].ReasoningEffort)
	}
}

type namedProvider struct {
	name     string
	models   []provider.ModelInfo
	controls []provider.GenerationControl
	calls    *[]string
}

func (p *namedProvider) Stream(context.Context, provider.Request) (provider.Stream, error) {
	*p.calls = append(*p.calls, p.name)
	return provider.StreamFunc(provider.Chunk{Done: true, FinishReason: "stop"}), nil
}

func (p *namedProvider) Models(context.Context) ([]provider.ModelInfo, error) { return p.models, nil }

func (p *namedProvider) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return p.controls, nil
}

func TestHotProviderSwapsCapabilities(t *testing.T) {
	var calls []string
	h := newHotProvider(&namedProvider{
		name:     "first",
		models:   []provider.ModelInfo{{ID: "m1", EndpointKind: provider.EndpointChatCompletions}},
		controls: []provider.GenerationControl{provider.GenerationControlGenerationBudget},
		calls:    &calls,
	})

	if _, err := h.Stream(context.Background(), provider.Request{}); err != nil {
		t.Fatalf("first Stream() error = %v, want nil", err)
	}
	models, err := h.Models(context.Background())
	if err != nil {
		t.Fatalf("first Models() error = %v, want nil", err)
	}
	if len(models) != 1 || models[0].ID != "m1" {
		t.Fatalf("first Models() = %v, want [m1]", models)
	}
	honored, err := provider.NegotiateGenerationControls(context.Background(), h, []provider.ControlRequirement{{
		Control: provider.GenerationControlThinkingSuppression, Required: false,
	}})
	if err != nil {
		t.Fatalf("first NegotiateGenerationControls() error = %v, want nil", err)
	}
	if len(honored) != 0 {
		t.Fatalf("first honored controls = %v, want none", honored)
	}

	h.Set(&namedProvider{
		name:     "second",
		models:   []provider.ModelInfo{{ID: "m2", EndpointKind: provider.EndpointChatCompletions}},
		controls: []provider.GenerationControl{provider.GenerationControlThinkingSuppression},
		calls:    &calls,
	})
	if _, err := h.Stream(context.Background(), provider.Request{}); err != nil {
		t.Fatalf("second Stream() error = %v, want nil", err)
	}
	models, err = h.Models(context.Background())
	if err != nil {
		t.Fatalf("second Models() error = %v, want nil", err)
	}
	if len(models) != 1 || models[0].ID != "m2" {
		t.Fatalf("second Models() = %v, want [m2]", models)
	}
	honored, err = provider.NegotiateGenerationControls(context.Background(), h, []provider.ControlRequirement{{
		Control: provider.GenerationControlThinkingSuppression, Required: false,
	}})
	if err != nil {
		t.Fatalf("second NegotiateGenerationControls() error = %v, want nil", err)
	}
	if len(honored) != 1 || honored[0] != provider.GenerationControlThinkingSuppression {
		t.Fatalf("second honored controls = %v, want [thinking_suppression]", honored)
	}
	if want := []string{"first", "second"}; len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("stream call order = %v, want %v", calls, want)
	}
}
