package tui

import (
	tea "charm.land/bubbletea/v2"
)

// ToolFeed bridges a live run's tool-call stream into the TUI's rendering loop
// (issue #84). The app's engine listener writes each ToolCallEvent as a start
// update and each ToolResultEvent as a result update here, and the model drains
// them on the UI goroutine via a waiting command, rendering a compact one-line
// `⊕ tool  args` entry per tool with a collapsed result summary that never
// silently drops the full output (the expand path reveals it). It is read-only
// against the agent loop: writes are non-blocking so a busy run never stalls.
type ToolFeed struct {
	// updates is the buffered feed the app's engine listener writes to; the
	// model drains it on the UI goroutine (toolWait -> toolUpdateMsg).
	updates chan ToolUpdate
}

// ToolUpdate is one tool-call observation for the transcript. It mirrors the
// engine seam's ToolCallEvent / ToolResultEvent while keeping the TUI decoupled
// from the engine package. Exactly one of Start or Result is set.
type ToolUpdate struct {
	// Start is set when a tool began executing (provided the tool name/args).
	Start *ToolStart
	// Result is set when a tool finished (provided the result + compression +
	// line-delta metadata). It pairs with the most recent Start.
	Result *ToolResult
}

// ToolStart is the leading half of a tool entry: the tool began executing.
type ToolStart struct {
	// Name is the tool invoked, e.g. "edit", "bash", "read".
	Name string
	// Args is the raw JSON arguments string the tool was invoked with.
	Args string
}

// ToolResult is the trailing half of a tool entry: the tool's delivered result
// plus the deterministic compression and line-delta metadata the TUI renders
// (docs/spec.md §5, issue #84).
type ToolResult struct {
	// Name is the tool that ran (matches its ToolStart).
	Name string
	// Result is the full delivered result string, uncompressed; it backs the
	// expand-to-full-result path (nothing is silently truncated).
	Result string
	// Lines is the count of lines in the delivered result, including any
	// explicit "+N more" marker line when present.
	Lines int
	// Dropped is the number of content lines hidden behind the "+N more" tail.
	Dropped int
	// Compressed is true when the delivered result carries the "+N more" tail.
	Compressed bool
	// Added is the line delta a file-mutating edit added (0 for non-edits).
	Added int
	// Removed is the line delta a file-mutating edit removed (0 for non-edits).
	Removed int
	// Before is the target file's full content before a file-mutating tool ran
	// (empty when the run did not report it). It backs the review panel's inline
	// diff of a changed file (issue #90).
	Before string
	// After is the target file's full content after a file-mutating tool ran.
	After string
	// Path is the host filesystem path of the target file, backing the review
	// panel's open_in_browser escape hatch (issue #90).
	Path string
}

// NewToolFeed builds a live tool feed ready to be handed to a Model via
// Dependencies. The caller wires the engine event seam's tool events into
// UpdateChan.
func NewToolFeed() *ToolFeed {
	return &ToolFeed{updates: make(chan ToolUpdate, 64)}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (f *ToolFeed) UpdateChan() chan<- ToolUpdate { return f.updates }

// Updates exposes the same feed for reading (tests/observation).
func (f *ToolFeed) Updates() <-chan ToolUpdate { return f.updates }

// toolUpdateMsg carries one tool-call observation into the UI loop. It is
// produced by the waiting command (toolWait) so the transcript's tool entries
// appear live as the run streams, even with no keyboard input.
type toolUpdateMsg struct {
	update ToolUpdate
}

// toolWait returns a command that blocks until the next tool-call update
// arrives on the engine-seam channel, then delivers it to the UI loop as a
// toolUpdateMsg. The model re-issues it after each update so tool entries keep
// streaming (issue #84). When the channel closes it returns nil so polling stops.
func toolWait(f *ToolFeed) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-f.updates
		if !ok {
			return nil
		}
		return toolUpdateMsg{update: u}
	}
}
