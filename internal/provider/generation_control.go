package provider

import (
	"context"
	"fmt"
)

// GenerationControl identifies one provider-side generation control a special turn may request on the wire.
type GenerationControl string

// The five generation controls a provider can declare support for and a special turn can request.
const (
	GenerationControlJSONObjectMode        GenerationControl = "json_object_mode"
	GenerationControlGenerationBudget      GenerationControl = "generation_budget"
	GenerationControlSamplingPolicy        GenerationControl = "sampling_policy"
	GenerationControlToolSchemaEnforcement GenerationControl = "tool_schema_enforcement"
	GenerationControlThinkingSuppression   GenerationControl = "thinking_suppression"
)

// ControlRequirement pairs a generation control with how it must be honored.
type ControlRequirement struct {
	Control  GenerationControl
	Required bool
}

// GenerationControlProvider is an optional capability a Provider may expose: declaring which generation controls it can honor on the wire.
type GenerationControlProvider interface {
	SupportedGenerationControls(ctx context.Context) ([]GenerationControl, error)
}

// UnsupportedRequiredControlError reports that a special turn declared a generation control as required but the provider cannot honor it.
type UnsupportedRequiredControlError struct {
	Control GenerationControl
}

// Error implements error.
func (e *UnsupportedRequiredControlError) Error() string {
	return fmt.Sprintf("provider does not support required generation control %q", string(e.Control))
}

// NegotiateGenerationControls pre-flights a set of control requirements against a provider's declared capability, returning the controls that will be honored.
func NegotiateGenerationControls(ctx context.Context, p Provider, reqs []ControlRequirement) ([]GenerationControl, error) {
	supported := setOf(nil)
	if gp, ok := p.(GenerationControlProvider); ok {
		declared, err := gp.SupportedGenerationControls(ctx)
		if err != nil {
			return nil, err
		}
		supported = setOf(declared)
	}

	honoredByControl := map[GenerationControl]ControlRequirement{}
	order := []GenerationControl{}
	for _, r := range reqs {
		if _, seen := honoredByControl[r.Control]; !seen {
			order = append(order, r.Control)
		}
		if r.Required {
			honoredByControl[r.Control] = r
		} else if _, seen := honoredByControl[r.Control]; !seen {
			honoredByControl[r.Control] = r
		}
	}

	honored := []GenerationControl{}
	for _, c := range order {
		req := honoredByControl[c]
		if !supported[c] {
			if req.Required {
				return nil, &UnsupportedRequiredControlError{Control: c}
			}
			continue // observable degradation: the optional control is dropped
		}
		honored = append(honored, c)
	}
	return honored, nil
}

// setOf builds a membership set from a slice of generation controls.
func setOf(controls []GenerationControl) map[GenerationControl]bool {
	m := make(map[GenerationControl]bool, len(controls))
	for _, c := range controls {
		m[c] = true
	}
	return m
}
