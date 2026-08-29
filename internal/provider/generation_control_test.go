package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type capableProvider struct {
	Scripted
	supported []GenerationControl
}

func (c *capableProvider) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return append([]GenerationControl(nil), c.supported...), nil
}

func TestNegotiateAllSupported(t *testing.T) {
	t.Parallel()
	p := &capableProvider{supported: []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlToolSchemaEnforcement,
		GenerationControlThinkingSuppression,
	}}
	reqs := []ControlRequirement{
		{Control: GenerationControlToolSchemaEnforcement, Required: true},
		{Control: GenerationControlThinkingSuppression, Required: false},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	want := []string{string(GenerationControlToolSchemaEnforcement), string(GenerationControlThinkingSuppression)}
	if !sameControls(got, want) {
		t.Fatalf("NegotiateGenerationControls() = %v, want %v", got, want)
	}
}

type failingProvider struct {
	Scripted
}

func (f *failingProvider) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return nil, errors.New("capability query failed")
}

func TestNegotiateCapabilityErrorPropagates(t *testing.T) {
	t.Parallel()
	reqs := []ControlRequirement{{Control: GenerationControlThinkingSuppression, Required: false}}
	_, err := NegotiateGenerationControls(context.Background(), &failingProvider{}, reqs)
	if err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want capability query failure")
	}
	if !strings.Contains(err.Error(), "capability query failed") {
		t.Fatalf("NegotiateGenerationControls() error = %v, want capability query failure", err)
	}
}

func TestNegotiateUnsupportedRequiredFails(t *testing.T) {
	t.Parallel()
	p := &capableProvider{supported: []GenerationControl{GenerationControlToolSchemaEnforcement}}
	reqs := []ControlRequirement{
		{Control: GenerationControlThinkingSuppression, Required: true},
		{Control: GenerationControlToolSchemaEnforcement, Required: true},
	}
	_, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want unsupported-required error")
	}
	if !isUnsupportedRequired(err, GenerationControlThinkingSuppression) {
		t.Fatalf("error = %v, want unsupported-required for Thinking Suppression", err)
	}
	if !strings.Contains(err.Error(), string(GenerationControlThinkingSuppression)) {
		t.Fatalf("error = %q, want it to name Thinking Suppression", err.Error())
	}
}

func TestNegotiateUnsupportedOptionalDegrades(t *testing.T) {
	t.Parallel()
	p := &capableProvider{supported: []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlThinkingSuppression,
	}}
	reqs := []ControlRequirement{
		{Control: GenerationControlToolSchemaEnforcement, Required: false}, // unsupported, optional
		{Control: GenerationControlGenerationBudget, Required: false},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	want := []string{string(GenerationControlGenerationBudget)}
	if !sameControls(got, want) {
		t.Fatalf("NegotiateGenerationControls() = %v, want %v (tool-schema enforcement dropped)", got, want)
	}
}

func TestNegotiateProviderWithoutCapability(t *testing.T) {
	t.Parallel()
	p := NewScripted(nil)
	reqs := []ControlRequirement{
		{Control: GenerationControlGenerationBudget, Required: false},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil (optional dropped)", err)
	}
	if len(got) != 0 {
		t.Fatalf("NegotiateGenerationControls() = %v, want none honored without capability", got)
	}

	reqs = []ControlRequirement{{Control: GenerationControlGenerationBudget, Required: true}}
	if _, err := NegotiateGenerationControls(context.Background(), p, reqs); err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want unsupported-required error")
	}
}

func TestNegotiateDeduplicatesRepeatedControls(t *testing.T) {
	t.Parallel()
	p := &capableProvider{supported: []GenerationControl{GenerationControlThinkingSuppression}}
	reqs := []ControlRequirement{
		{Control: GenerationControlThinkingSuppression, Required: false},
		{Control: GenerationControlThinkingSuppression, Required: true},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	want := []string{string(GenerationControlThinkingSuppression)}
	if !sameControls(got, want) {
		t.Fatalf("NegotiateGenerationControls() = %v, want %v", got, want)
	}
}

func sameControls(got []GenerationControl, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if string(got[i]) != w {
			return false
		}
	}
	return true
}

func isUnsupportedRequired(err error, ctrl GenerationControl) bool {
	var ue *UnsupportedRequiredControlError
	return errors.As(err, &ue) && ue.Control == ctrl
}
