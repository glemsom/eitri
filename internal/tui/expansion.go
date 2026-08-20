package tui

// expansionKey identifies one collapsible block inside ExpansionState's force
// table: the block kind plus the id the caller owns (a reasoning fragment
// index or a tool-entry log index). The module holds no reference to transcripts
// or tool logs; callers map their own blocks onto ids.
type expansionKey struct {
	kind blockKind
	id   int
}

// ExpansionState owns Eitri's block-expansion policy: the global view mode plus
// a per-block expansion force for both reasoning fragments and tool-result
// entries. Every block's open/collapsed decision flows through one query,
// expanded(kind, id); the toggle/set/clear/setMode operations mutate that single
// policy source, so one owner (and one test surface) replaces the scattered
// leaf flags the callers used before migrating onto the seam.
type ExpansionState struct {
	mode         viewMode
	cotExpanded  bool // reasoning blocks render expanded by default
	toolExpanded bool // tool-result entries render expanded by default
	forces       map[expansionKey]bool
}

// NewExpansionState returns an ExpansionState with the given global mode and
// per-kind collapsed-by-default flags and no per-block forces.
func NewExpansionState(mode viewMode, cotExpanded, toolExpanded bool) ExpansionState {
	return ExpansionState{mode: mode, cotExpanded: cotExpanded, toolExpanded: toolExpanded}
}

// expanded reports whether the block identified by kind and id renders open: a
// per-block force wins over the global mode, the expand-all/collapse-all modes
// decide for a block with no force, and otherwise the block follows its kind's
// collapsed-by-default flag.
func (e ExpansionState) expanded(kind blockKind, id int) bool {
	if f, ok := e.forces[expansionKey{kind, id}]; ok {
		return f
	}
	switch e.mode {
	case viewExpandAll:
		return true
	case viewCollapseAll:
		return false
	}
	switch kind {
	case blockReasoning:
		return e.cotExpanded
	case blockTool:
		return e.toolExpanded
	}
	return false
}

// toggle forces the block to the opposite of how it currently renders, the
// Enter-on-focused-block interaction: an open block collapses and a collapsed
// block expands, without disturbing any other block.
func (e *ExpansionState) toggle(kind blockKind, id int) {
	e.set(kind, id, !e.expanded(kind, id))
}

// forceFor reports the per-block force pinned on the block, if any: the value
// and whether a force exists at all. It lets a caller distinguish a pinned
// force from the mode/default fallback, e.g. the Ctrl+E collapse-pin toggle
// that flips a block between force-collapsed and unpinned.
func (e ExpansionState) forceFor(kind blockKind, id int) (force, ok bool) {
	f, ok := e.forces[expansionKey{kind, id}]
	return f, ok
}

// clearForcesOf drops every per-block force whose value matches forceValue: the
// collapse-direction forces (value false) or the expand-direction forces (value
// true). It is the per-direction half of setMode's clear-all, used by the
// transcript's expand-all / collapse-all toggles, which clear only the opposing
// direction so a manually pinned force in the other direction survives the mode
// round-trip.
func (e *ExpansionState) clearForcesOf(forceValue bool) {
	for k, v := range e.forces {
		if v == forceValue {
			delete(e.forces, k)
		}
	}
}

// set pins a single block's expansion decision with an explicit force.
func (e *ExpansionState) set(kind blockKind, id int, expanded bool) {
	if e.forces == nil {
		e.forces = map[expansionKey]bool{}
	}
	e.forces[expansionKey{kind, id}] = expanded
}

// clear drops one block's force so it returns to the mode/default decision.
func (e *ExpansionState) clear(kind blockKind, id int) {
	delete(e.forces, expansionKey{kind, id})
}

// setMode changes the global expansion mode, dropping every per-block force
// when the mode becomes expand-all or collapse-all so the mode rules uniformly.
func (e *ExpansionState) setMode(mode viewMode) {
	e.mode = mode
	if mode == viewExpandAll || mode == viewCollapseAll {
		e.clearForces()
	}
}

// clearForces drops every per-block force, returning all blocks to the mode and
// collapsed-by-default decision.
func (e *ExpansionState) clearForces() {
	e.forces = nil
}
