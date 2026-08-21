package tui

// expansionKey identifies one collapsible block inside ExpansionState's force
// table: the block kind plus the id the caller owns (a reasoning fragment
// index or a tool-entry log index). The module holds no reference to transcripts
// or tool logs; callers map their own blocks onto ids.
type expansionKey struct {
	kind blockKind
	id   int
}

// expansionConfig is the explicit config bundle every expansion decision
// takes: the caller's current global view mode plus each block kind's
// collapsed-by-default default. The decision reads only this bundle and the
// per-block force table — no stored mode or defaults — so a hit-test can never
// disagree with how the block rendered.
type expansionConfig struct {
	mode         viewMode
	cotExpanded  bool // reasoning blocks render expanded by default
	toolExpanded bool // tool-result entries render expanded by default
}

// reasoningWholeID is the ExpansionState id a turn's whole-block reasoning
// force is keyed on (the migrated thinkingExpanded / thinkingCollapsed flags),
// distinct from the per-fragment reasoning ids (0..n) so a whole-turn toggle
// never collides with the first fragment's pin. A callers-owned negative id is
// safe because fragment indices are always non-negative.
const reasoningWholeID = -1

// ExpansionState owns Eitri's block-expansion policy: a per-block expansion
// force for both reasoning fragments and tool-result entries. Every block's
// open/collapsed decision flows through one pure query, expanded(kind, id, cfg),
// which reads the explicit config bundle and this force table — nothing is
// stored besides the forces, so a hit-test can never disagree with how a block
// rendered.
type ExpansionState struct {
	forces map[expansionKey]bool
}

// expanded reports whether the block identified by kind and id renders open:
// a per-block force wins over the config's global mode, the expand-all/collapse-all
// modes decide for a block with no force, and otherwise the block follows its kind's
// collapsed-by-default flag from cfg. The decision is pure: it reads only cfg
// and the force table.
func (e ExpansionState) expanded(kind blockKind, id int, cfg expansionConfig) bool {
	if f, ok := e.forces[expansionKey{kind, id}]; ok {
		return f
	}
	switch cfg.mode {
	case viewExpandAll:
		return true
	case viewCollapseAll:
		return false
	}
	switch kind {
	case blockReasoning:
		return cfg.cotExpanded
	case blockTool:
		return cfg.toolExpanded
	}
	return false
}

// toggle forces the block to the opposite of how it currently renders under
// cfg, the Enter-on-focused-block interaction: an open block collapses and a
// collapsed block expands, without disturbing any other block.
func (e *ExpansionState) toggle(kind blockKind, id int, cfg expansionConfig) {
	e.set(kind, id, !e.expanded(kind, id, cfg))
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
// true). It is the per-direction half of a global-mode entry's clear-all, used by the
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

// clearReasoningFragments drops every per-fragment reasoning force (ids >= 0),
// leaving the whole-block force (id < 0) intact — the turn-commit cleanup that
// discards a live turn's fragment pins once its chain-of-thought collapses to a
// single committed block.
func (e *ExpansionState) clearReasoningFragments() {
	for k := range e.forces {
		if k.kind == blockReasoning && k.id >= 0 {
			delete(e.forces, k)
		}
	}
}
