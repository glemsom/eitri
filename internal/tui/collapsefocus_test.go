package tui

import "testing"

// TestCollapseFocus_NoBlocksStaysOff locks the no-op edge: with an empty
// collapsible-block list the cursor must never activate, regardless of how many
// times Tab is pressed.
func TestCollapseFocus_NoBlocksStaysOff(t *testing.T) {
	var f collapseFocus
	f.focusNext(0)
	if _, ok := f.focusedIndex(0); ok {
		t.Fatalf("focus must stay off with zero blocks, got ok=%v", ok)
	}
	f.focusNext(0)
	if _, ok := f.focusedIndex(0); ok {
		t.Fatalf("focus must stay off with zero blocks after repeated tabs, got ok=%v", ok)
	}
}

// TestCollapseFocus_FirstTabActivatesCursor locks the activation rule: the
// first Tab turns the cross off and lands the cursor on the first block.
func TestCollapseFocus_FirstTabActivatesCursor(t *testing.T) {
	var f collapseFocus
	if _, ok := f.focusedIndex(2); ok {
		t.Fatalf("cursor must be idle before any Tab, got ok=%v", ok)
	}
	f.focusNext(2)
	idx, ok := f.focusedIndex(2)
	if !ok || idx != 0 {
		t.Fatalf("first Tab must focus block 0, got idx=%d ok=%v", idx, ok)
	}
}

// TestCollapseFocus_CyclesAndWraps locks the Tab traversal: each focusNext
// advances the cursor by one and, past the last block, wraps back to the first.
func TestCollapseFocus_CyclesAndWraps(t *testing.T) {
	var f collapseFocus
	f.focusNext(3)
	for want := 1; want < 4; want++ {
		f.focusNext(3)
		idx, ok := f.focusedIndex(3)
		if !ok || idx != want%3 {
			t.Fatalf("after advancing to want=%d got idx=%d ok=%v", want, idx, ok)
		}
	}
	if !f.focusedIs(3, 0) {
		t.Fatalf("after wrapping the cursor must be back on block 0")
	}
}

// TestCollapseFocus_InvalidCursorWhenListShrinks locks the safety net: when the
// block list shrinks so the cursor no longer indexes a block, the seam reports
// no focused block (the transcript then treats it as unfocused) instead of
// panicking or wrapping unexpectedly.
func TestCollapseFocus_InvalidCursorWhenListShrinks(t *testing.T) {
	var f collapseFocus
	f.focusNext(3) // cursor lands on 0
	f.focusNext(3) // cursor lands on 1
	f.focusNext(3) // cursor lands on 2
	if _, ok := f.focusedIndex(2); ok {
		t.Fatalf("cursor 2 must be invalid for a 2-block list, got ok=%v", ok)
	}
	if f.focusedIs(2, 2) {
		t.Fatalf("focusedIs must be false for an out-of-range block, got true")
	}
	// A subsequent Tab re-clamps to a valid position within the new list.
	f.focusNext(2)
	if idx, ok := f.focusedIndex(2); !ok || idx != 1 {
		t.Fatalf("Tab after shrink must re-clamp to a valid block, got idx=%d ok=%v", idx, ok)
	}
}
