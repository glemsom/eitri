package tui

// collapseFocus owns the block-focus cursor state that drives the per-block
// Tab/Enter interaction: whether the cursor is active at all (on) and which
// collapsible block it points at (cursor), as an index into a caller-supplied
// block list. It is a pure value type — a Transcript renders its own
// collapsible-block list onto it (feeding the block count into focusNext and
// querying the cursor position via focusedIndex) — so the Tab-cycle state
// machine is unit-testable with no Transcript scaffolding. It holds no
// reference to transcripts, messages, or tool logs.
type collapseFocus struct {
	on     bool
	cursor int
}

// focusNext advances the cursor to the next collapsible block, wrapping. The
// first call activates the cursor on the first block; a block list of zero
// length leaves (or returns) the cursor off; a cursor left pointing past a
// shrunk list re-clamps to a valid position through the wrapping advance.
func (f *collapseFocus) focusNext(blockCount int) {
	if blockCount == 0 {
		f.on = false
		f.cursor = 0
		return
	}
	if !f.on {
		f.on = true
		f.cursor = 0
		return
	}
	f.cursor = (f.cursor + 1) % blockCount
}

// focusedIndex returns the cursor's position when it is active and valid for a
// blockCount-length list, else ok=false. It is the seam's single query: the
// transcript and renderer use it to resolve where the cursor lands.
func (f collapseFocus) focusedIndex(blockCount int) (idx int, ok bool) {
	if !f.on || f.cursor < 0 || f.cursor >= blockCount {
		return 0, false
	}
	return f.cursor, true
}

// focusedIs reports whether the block at position idx is the one under the
// cursor, for a blockCount-length list.
func (f collapseFocus) focusedIs(blockCount, idx int) bool {
	cur, ok := f.focusedIndex(blockCount)
	return ok && cur == idx
}
