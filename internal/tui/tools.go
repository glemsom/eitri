package tui

import (
	tea "charm.land/bubbletea/v2"
)

// ToolFeed bridges a live run's tool-call stream into the TUI's rendering loop.
type ToolFeed struct {
	updates chan ToolUpdate
}

// ToolUpdate is one tool-call observation for the transcript.
type ToolUpdate struct {
	Start  *ToolStart
	Result *ToolResult
}

// ToolStart is the leading half of a tool entry: the tool began executing.
type ToolStart struct {
	Name string
	Args string
}

// ToolResult is the trailing half of a tool entry: the tool's delivered result plus the deterministic compression metadata the TUI renders and the file line-delta / before-after content computed by the TUI-side delta observer from the paired tool start/result events — the source of the `⊕ edit path [+N,-M]` tag and the card diff's inline diff.
type ToolResult struct {
	Name         string
	Result       string
	BytesDropped int
	Lines        int
	Dropped      int
	Compressed   bool
	Added        int
	Removed      int
	Before       string
	After        string
	Path         string
}

// NewToolFeed builds a live tool feed ready to be handed to a Model via Dependencies.
func NewToolFeed() *ToolFeed {
	return &ToolFeed{updates: make(chan ToolUpdate, 64)}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (f *ToolFeed) UpdateChan() chan<- ToolUpdate { return f.updates }

// Updates exposes the same feed for reading (tests/observation).
func (f *ToolFeed) Updates() <-chan ToolUpdate { return f.updates }

// toolUpdateMsg carries one tool-call observation into the UI loop.
type toolUpdateMsg struct {
	update ToolUpdate
}

// toolWait returns a command that blocks until the next tool-call update arrives on the engine-seam channel, then delivers it to the UI loop as a toolUpdateMsg.
func toolWait(f *ToolFeed) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-f.updates
		if !ok {
			return nil
		}
		return toolUpdateMsg{update: u}
	}
}
