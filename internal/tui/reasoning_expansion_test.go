package tui

import (
	"strings"
	"testing"
)

// TestThinkingExpandedForFrag_OwnsForcesOnSeam locks issue #469's reasoning
// migration: the whole-block and per-fragment reasoning forces live on the
// message's ExpansionState seam, and the open/collapsed decision reads through
// it rather than from scattered leaf flags. A pinned whole-block force beats
// any global mode; a per-fragment force beats the whole-block decision.
func TestThinkingExpandedForFrag_OwnsForcesOnSeam(t *testing.T) {
	t.Parallel()

	// Whole-block force-expand beats the collapse-all mode and the
	// collapsed-by-default flag.
	msg := message{expansion: expansionWithReasoningForces(true, false)}
	if !thinkingExpandedForFrag(msg, 0, viewCollapseAll, false) {
		t.Errorf("whole-block force-expand must win over collapse-all, got collapsed")
	}

	// No force: the mode and collapsed-by-default flag decide.
	msg = message{}
	if thinkingExpandedForFrag(msg, 0, viewCollapseAll, true) {
		t.Errorf("collapse-all must collapse a force-less block despite the expanded default")
	}
	if !thinkingExpandedForFrag(msg, 0, viewExpandAll, false) {
		t.Errorf("expand-all must expand a force-less block despite the collapsed default")
	}

	// A per-fragment force beats the whole-block force for its own fragment.
	msg = message{expansion: expansionWithReasoningForces(false, true)} // whole-block force-collapse
	msg.expansion.set(blockReasoning, 0, true)                          // fragment 0 pinned force-expand
	if !thinkingExpandedForFrag(msg, 0, viewDefault, false) {
		t.Errorf("fragment 0's force-expand must beat the whole-block force-collapse")
	}
	if thinkingExpandedForFrag(msg, 1, viewDefault, false) {
		t.Errorf("fragment 1 (no own force) must follow the whole-block force-collapse, got expanded")
	}
}

// TestReasoning_expansionSeamOwnsWholeBlockForces locks that a committed turn's
// reasoning-block toggle routes through the ExpansionState seam: the Transcript
// delegates the focus-driven toggle to toggleThinkingFragment, which pins the
// seam force so the open/collapsed decision flips between force-expand and
// force-collapse with no leaf flags left on the message.
func TestReasoning_expansionSeamOwnsWholeBlockForces(t *testing.T) {
	t.Parallel()
	tx := committedReasoningFlowTranscript("the body", "answer")

	if _, ok := tx.messages[1].expansion.forceFor(blockReasoning, 0); ok {
		t.Fatalf("fresh committed block must carry no fragment force, got a force present")
	}

	tx.toggleThinkingFragment(1, 0) // collapsed default -> force-expand
	if f, ok := tx.messages[1].expansion.forceFor(blockReasoning, 0); !ok || !f {
		t.Errorf("toggle must pin a force-expand on the seam, got ok=%v force=%v", ok, f)
	}

	tx.toggleThinkingFragment(1, 0) // force-expand -> force-collapse
	if f, ok := tx.messages[1].expansion.forceFor(blockReasoning, 0); !ok || f {
		t.Errorf("second toggle must flip to force-collapse on the seam, got ok=%v force=%v", ok, f)
	}
}

// TestReasoning_renderDelegatesToSeam drives the full committed render path and
// confirms the open/collapsed body follows the seam force rather than the old
// thinkingExpanded flag.
func TestReasoning_renderDelegatesToSeam(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	tx := committedReasoningFlowTranscript("the body", "answer")
	var hidden strings.Builder
	tx.renderHistory(&hidden, nil, nil)
	if strings.Contains(ansiStrip(hidden.String()), "the body") {
		t.Fatalf("force-less committed block must render collapsed by default:\n%s", hidden.String())
	}

	tx.messages[1].expansion.set(blockReasoning, reasoningWholeID, true)
	var shown strings.Builder
	tx.renderHistory(&shown, nil, nil)
	if !strings.Contains(ansiStrip(shown.String()), "the body") {
		t.Errorf("force-expanded committed block must render its body:\n%s", shown.String())
	}
}

// TestReasoning_clearCollapseForcesDirectional locks that expand-all / collapse-all
// route through the seam and clear only the opposing force direction, so a
// manually pinned force in the other direction survives a mode round-trip (the
// same policy the tool-log migration #470 establishes).
func TestReasoning_clearCollapseForcesDirectional(t *testing.T) {
	t.Parallel()
	tx := committedReasoningFlowTranscript("", "answer")
	msg := &tx.messages[1].expansion

	// Pin a whole-block force-collapse, a fragment force-expand, and a tool
	// collapse force on the shared seam.
	msg.set(blockReasoning, reasoningWholeID, false)
	msg.set(blockReasoning, 2, true)
	tx.log.expansion.set(blockTool, 0, false)

	tx.clearCollapseForces()

	// collapse-direction forces are gone...
	if _, ok := msg.forceFor(blockReasoning, reasoningWholeID); ok {
		t.Errorf("clearCollapseForces must drop the whole-block force-collapse")
	}
	if _, ok := tx.log.expansion.forceFor(blockTool, 0); ok {
		t.Errorf("clearCollapseForces must drop the tool force-collapse")
	}
	// ...but the expand-direction fragment force survives.
	if f, ok := msg.forceFor(blockReasoning, 2); !ok || !f {
		t.Errorf("clearCollapseForces must keep the expand-direction fragment force, got ok=%v force=%v", ok, f)
	}
}

// TestReasoning_clearReasoningFragmentsKeepsWholeBlock locks the turn-commit
// cleanup: it drops a live turn's per-fragment reasoning pins while preserving
// the whole-block force, so a collapsed committed block stays collapsed.
func TestReasoning_clearReasoningFragmentsKeepsWholeBlock(t *testing.T) {
	t.Parallel()
	tx := committedReasoningFlowTranscript("", "answer")
	msg := &tx.messages[1].expansion
	msg.set(blockReasoning, reasoningWholeID, false) // whole-block force-collapse
	msg.set(blockReasoning, 0, true)                 // fragment 0 force-expand
	msg.set(blockReasoning, 2, false)                // fragment 2 force-collapse

	tx.clearReasoningFragments(1)

	if _, ok := msg.forceFor(blockReasoning, 0); ok {
		t.Errorf("clearReasoningFragments must drop fragment 0's force")
	}
	if _, ok := msg.forceFor(blockReasoning, 2); ok {
		t.Errorf("clearReasoningFragments must drop fragment 2's force")
	}
	if f, ok := msg.forceFor(blockReasoning, reasoningWholeID); !ok || f {
		t.Errorf("clearReasoningFragments must keep the whole-block force-collapse, got ok=%v force=%v", ok, f)
	}
}

// TestReasoning_clearReasoningExpandForce drops a whole-block force-expand so a
// completed turn auto-collapses outside the expand-all mode.
func TestReasoning_clearReasoningExpandForce(t *testing.T) {
	t.Parallel()
	tx := committedReasoningFlowTranscript("", "answer")
	msg := &tx.messages[1].expansion

	msg.set(blockReasoning, reasoningWholeID, true) // a leftover force-expand
	tx.clearReasoningExpandForce(1)
	if _, ok := msg.forceFor(blockReasoning, reasoningWholeID); ok {
		t.Errorf("clearReasoningExpandForce must drop the whole-block force-expand")
	}

	msg.set(blockReasoning, reasoningWholeID, false) // a force-collapse must survive
	tx.clearReasoningExpandForce(1)
	if f, ok := msg.forceFor(blockReasoning, reasoningWholeID); !ok || f {
		t.Errorf("clearReasoningExpandForce must leave a force-collapse intact, got ok=%v force=%v", ok, f)
	}
}
