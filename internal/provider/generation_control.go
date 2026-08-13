package provider

import (
	"context"
	"fmt"
)

// This file defines the generation-control capability negotiation seam (issue
// #58 / docs/spec.md §13): how a special turn declares which provider-side
// generation controls it wants, and how higher layers learn which of those a
// provider can actually honor — before any wire call. The four controls are the
// constrained-output levers Eitri may request on internal (non-tool) turns:
// schema-constrained JSON, an output-token budget, a sampling policy, and
// provider-side tool-schema enforcement. Each is wire-emitted by the specific
// special-turn tickets (#59–#62); this seam is the negotiation contract they
// share.

// GenerationControl identifies one provider-side generation control a special
// turn may request on the wire.
type GenerationControl string

// The four generation controls a provider can declare support for and a
// special turn can request.
const (
	// GenerationControlJSONObjectMode requests schema-constrained JSON Object
	// Mode for the final answer (issue #59).
	GenerationControlJSONObjectMode GenerationControl = "json_object_mode"
	// GenerationControlGenerationBudget requests a hard per-turn output token
	// cap for an internal generation (issue #60).
	GenerationControlGenerationBudget GenerationControl = "generation_budget"
	// GenerationControlSamplingPolicy requests temperature- or nucleus-based
	// sampling (issue #61).
	GenerationControlSamplingPolicy GenerationControl = "sampling_policy"
	// GenerationControlToolSchemaEnforcement requests provider-side tool-schema
	// enforcement in the tool manifest (issue #62).
	GenerationControlToolSchemaEnforcement GenerationControl = "tool_schema_enforcement"
)

// ControlRequirement pairs a generation control with how it must be honored.
// A special turn marks each control it wants as either required or optional.
type ControlRequirement struct {
	Control GenerationControl
	// Required is true when the turn cannot proceed without the control: an
	// unsupported required control fails negotiation before any wire call.
	// Required is false when the turn can degrade: an unsupported optional
	// control is dropped and the turn proceeds without it.
	Required bool
}

// GenerationControlProvider is an optional capability a Provider may expose:
// declaring which generation controls it can honor on the wire. Higher layers
// consult it through NegotiateGenerationControls instead of inspecting model ids
// or endpoint strings. A Provider that does not implement this surface honors no
// generation controls.
type GenerationControlProvider interface {
	SupportedGenerationControls(ctx context.Context) ([]GenerationControl, error)
}

// UnsupportedRequiredControlError reports that a special turn declared a
// generation control as required but the provider cannot honor it. It is
// returned before any network call so a turn fails fast instead of degrading.
type UnsupportedRequiredControlError struct {
	Control GenerationControl
}

// Error implements error.
func (e *UnsupportedRequiredControlError) Error() string {
	return fmt.Sprintf("provider does not support required generation control %q", string(e.Control))
}

// NegotiateGenerationControls pre-flights a set of control requirements against
// a provider's declared capability, returning the controls that will be honored.
// It is the single seam a generation-control-aware turn consults before calling
// Stream:
//
//   - A required control the provider cannot honor returns an
//     *UnsupportedRequiredControlError immediately — before any wire call.
//   - An optional control the provider cannot honor is dropped; its absence from
//     the returned set is the observable degradation, so callers can see that the
//     turn ran without it.
//   - A provider without the GenerationControlProvider capability honors nothing:
//     any required control is an error, and optional controls are all dropped.
func NegotiateGenerationControls(ctx context.Context, p Provider, reqs []ControlRequirement) ([]GenerationControl, error) {
	supported := setOf(nil)
	if gp, ok := p.(GenerationControlProvider); ok {
		declared, err := gp.SupportedGenerationControls(ctx)
		if err != nil {
			return nil, err
		}
		supported = setOf(declared)
	}

	// Adopt the union of requirements, honoring each requested control once.
	// If the same control is listed both optional and required, required wins.
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
