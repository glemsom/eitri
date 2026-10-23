package tui

import "testing"

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
	f.focusNext(3)
	f.focusNext(3)
	f.focusNext(3)
	if _, ok := f.focusedIndex(2); ok {
		t.Fatalf("cursor 2 must be invalid for a 2-block list, got ok=%v", ok)
	}
	if f.focusedIs(2, 2) {
		t.Fatalf("focusedIs must be false for an out-of-range block, got true")
	}
	f.focusNext(2)
	if idx, ok := f.focusedIndex(2); !ok || idx != 1 {
		t.Fatalf("Tab after shrink must re-clamp to a valid block, got idx=%d ok=%v", idx, ok)
	}
}
