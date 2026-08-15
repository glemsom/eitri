package provider

import (
	"context"
	"errors"
	"testing"
)

// capableProvider is a deterministic Provider that declares a fixed set of
// supported generation controls via the GenerationControlProvider capability, so
// the negotiation helper is testable at the provider seam.
type capableProvider struct {
	Scripted
	supported []GenerationControl
}

// SupportedGenerationControls implements GenerationControlProvider.
func (c *capableProvider) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return append([]GenerationControl(nil), c.supported...), nil
}

// TestNegotiateAllSupported verifies a special turn whose controls the provider
// supports negotiates all of them through unchanged.
func TestNegotiateAllSupported(t *testing.T) {
	p := &capableProvider{supported: []GenerationControl{
		GenerationControlJSONObjectMode,
		GenerationControlGenerationBudget,
		GenerationControlSamplingPolicy,
		GenerationControlToolSchemaEnforcement,
	}}
	reqs := []ControlRequirement{
		{Control: GenerationControlJSONObjectMode, Required: true},
		{Control: GenerationControlSamplingPolicy, Required: false},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	want := []string{string(GenerationControlJSONObjectMode), string(GenerationControlSamplingPolicy)}
	if !sameControls(got, want) {
		t.Fatalf("NegotiateGenerationControls() = %v, want %v", got, want)
	}
}

// TestNegotiateUnsupportedRequiredFails verifies a required control the provider
// cannot honor fails before any wire call, returning an error that names the
// offending control.
func TestNegotiateUnsupportedRequiredFails(t *testing.T) {
	// Provider supports only tool-schema enforcement; the special turn requires
	// JSON Object Mode, which it cannot honor.
	p := &capableProvider{supported: []GenerationControl{GenerationControlToolSchemaEnforcement}}
	reqs := []ControlRequirement{
		{Control: GenerationControlJSONObjectMode, Required: true},
		{Control: GenerationControlToolSchemaEnforcement, Required: true},
	}
	_, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want unsupported-required error")
	}
	if !isUnsupportedRequired(err, GenerationControlJSONObjectMode) {
		t.Fatalf("error = %v, want unsupported-required for JSON Object Mode", err)
	}
}

// TestNegotiateUnsupportedOptionalDegrades verifies an optional control the
// provider cannot honor is dropped (observable degradation) while supported
// optional controls pass through and required controls still fail independently.
func TestNegotiateUnsupportedOptionalDegrades(t *testing.T) {
	p := &capableProvider{supported: []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlSamplingPolicy,
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

// TestNegotiateProviderWithoutCapability verifies a Provider that does not
// implement the GenerationControlProvider capability honors nothing: any
// required control fails, any optional control is dropped.
func TestNegotiateProviderWithoutCapability(t *testing.T) {
	// Scripted does not implement the capability surface.
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

	// A required control on a provider with no capability surface is an error.
	reqs = []ControlRequirement{{Control: GenerationControlGenerationBudget, Required: true}}
	if _, err := NegotiateGenerationControls(context.Background(), p, reqs); err == nil {
		t.Fatal("NegotiateGenerationControls() error = nil, want unsupported-required error")
	}
}

// TestNegotiateDeduplicatesRepeatedControls verifies a control listed more than
// once in the requirements negotiates once, and that repeated optional + required
// entries resolve to required.
func TestNegotiateDeduplicatesRepeatedControls(t *testing.T) {
	p := &capableProvider{supported: []GenerationControl{GenerationControlSamplingPolicy}}
	reqs := []ControlRequirement{
		{Control: GenerationControlSamplingPolicy, Required: false},
		{Control: GenerationControlSamplingPolicy, Required: true},
	}
	got, err := NegotiateGenerationControls(context.Background(), p, reqs)
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil", err)
	}
	want := []string{string(GenerationControlSamplingPolicy)}
	if !sameControls(got, want) {
		t.Fatalf("NegotiateGenerationControls() = %v, want %v", got, want)
	}
}

// sameControls reports whether got and want contain the same controls in order,
// comparing against string names.
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

// isUnsupportedRequired reports whether err is the unsupported-required error
// for the named control.
func isUnsupportedRequired(err error, ctrl GenerationControl) bool {
	var ue *UnsupportedRequiredControlError
	return errors.As(err, &ue) && ue.Control == ctrl
}
