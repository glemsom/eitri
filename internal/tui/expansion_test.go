package tui

import "testing"

// expansionForce is one per-block expansion force fed to the ExpansionState
// table test: the block kind, its id, and the expanded state the force pins.
type expansionForce struct {
	kind  blockKind
	id    int
	value bool
}

// stateFrom builds an ExpansionState from compact table inputs, applying any
// per-block forces on top of the mode and collapsed-by-default flags.
func stateFrom(mode viewMode, cotExpanded, toolExpanded bool, forces ...expansionForce) ExpansionState {
	s := NewExpansionState(mode, cotExpanded, toolExpanded)
	for _, f := range forces {
		s.set(f.kind, f.id, f.value)
	}
	return s
}

// TestExpansionState_expandedDecisions locks the module's state machine with a
// table of (mode × defaults × per-block forces) → open/collapsed, the single
// test surface issue #468 establishes before any caller migrates onto the seam.
func TestExpansionState_expandedDecisions(t *testing.T) {
	const reasoningID = 1
	const toolID = 2

	tests := []struct {
		name          string
		mode          viewMode
		cotExpanded   bool
		toolExpanded  bool
		forces        []expansionForce
		wantReasoning bool
		wantTool      bool
	}{
		// Default mode: each block follows its own collapsed-by-default flag.
		{name: "default both collapsed", mode: viewDefault, wantReasoning: false, wantTool: false},
		{name: "default cot expanded", mode: viewDefault, cotExpanded: true, wantReasoning: true, wantTool: false},
		{name: "default tool expanded", mode: viewDefault, toolExpanded: true, wantReasoning: false, wantTool: true},
		{name: "default both expanded", mode: viewDefault, cotExpanded: true, toolExpanded: true, wantReasoning: true, wantTool: true},

		// Global modes override the defaults for every block with no force.
		{name: "expand-all expands both despite default-collapsed", mode: viewExpandAll, wantReasoning: true, wantTool: true},
		{name: "collapse-all collapses both despite default-expanded", mode: viewCollapseAll, cotExpanded: true, toolExpanded: true, wantReasoning: false, wantTool: false},

		// A per-block force beats the global mode and is scoped to its own id.
		{name: "force-collapse reasoning beats expand-all", mode: viewExpandAll, cotExpanded: true, toolExpanded: true, forces: []expansionForce{{blockReasoning, reasoningID, false}}, wantReasoning: false, wantTool: true},
		{name: "force-expand tool beats collapse-all", mode: viewCollapseAll, forces: []expansionForce{{blockTool, toolID, true}}, wantReasoning: false, wantTool: true},
		{name: "force on one id leaves another to the default", mode: viewDefault, toolExpanded: true, forces: []expansionForce{{blockReasoning, reasoningID, false}}, wantReasoning: false, wantTool: true},
		{name: "force on reasoning does not affect tool default", mode: viewDefault, cotExpanded: true, forces: []expansionForce{{blockReasoning, reasoningID, true}}, wantReasoning: true, wantTool: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateFrom(tt.mode, tt.cotExpanded, tt.toolExpanded, tt.forces...)
			if got := s.expanded(blockReasoning, reasoningID); got != tt.wantReasoning {
				t.Errorf("expanded(reasoning, %d) = %v, want %v", reasoningID, got, tt.wantReasoning)
			}
			if got := s.expanded(blockTool, toolID); got != tt.wantTool {
				t.Errorf("expanded(tool, %d) = %v, want %v", toolID, got, tt.wantTool)
			}
		})
	}
}

// TestExpansionState_toggleFlipsOneBlock only pins the interaction contract:
// Enter on a focused block forces it to the opposite of how it currently
// renders, and never touches a sibling block's decision.
func TestExpansionState_toggleFlipsOneBlock(t *testing.T) {
	s := NewExpansionState(viewDefault, false, false) // both collapsed by default

	s.toggle(blockTool, 2) // collapsed → force-expand entry 2
	if !s.expanded(blockTool, 2) {
		t.Errorf("toggle must force-expand a collapsed entry, got collapsed")
	}
	if s.expanded(blockTool, 3) {
		t.Errorf("toggling entry 2 must not affect sibling entry 3")
	}

	s.toggle(blockTool, 2) // expanded → force-collapse entry 2 again
	if s.expanded(blockTool, 2) {
		t.Errorf("toggle must flip back to force-collapsed, got expanded")
	}
}

// TestExpansionState_setAndClear pins the explicit-force operations: set pins a
// block's decision and clear returns it to the module-level default.
func TestExpansionState_setAndClear(t *testing.T) {
	s := NewExpansionState(viewDefault, false, false)

	s.set(blockReasoning, 1, true)
	if !s.expanded(blockReasoning, 1) {
		t.Errorf("set(true) must force-expand the reasoning block")
	}

	s.clear(blockReasoning, 1)
	if s.expanded(blockReasoning, 1) {
		t.Errorf("clear must drop the force and return to the collapsed default")
	}
}

// TestExpansionState_globalModeEntryClearsForces locks that entering a global
// mode drops every per-block force so the mode rules uniformly, then a fresh
// toggle can re-pin a single block again.
func TestExpansionState_globalModeEntryClearsForces(t *testing.T) {
	s := NewExpansionState(viewExpandAll, false, false)
	s.set(blockReasoning, 1, false) // a stray force-collapse while in expand-all
	if s.expanded(blockReasoning, 1) {
		t.Fatalf("force-collapse must beat expand-all before the re-entry, got expanded")
	}

	s.setMode(viewExpandAll) // re-entering the global mode clears the force
	if !s.expanded(blockReasoning, 1) {
		t.Errorf("re-entering expand-all must drop the force so the block expands, got collapsed")
	}
}
