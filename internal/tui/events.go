package tui

import tea "charm.land/bubbletea/v2"

// StreamKind identifies which stream a StreamUpdate carries: chain-of-thought reasoning or assistant answer text.
type StreamKind int

const (
	ReasoningStream StreamKind = iota
	AnswerStream
)

// StreamUpdate is one additive delta for the transcript, on one of the two streams the engine emits: reasoning or answer text.
type StreamUpdate struct {
	Kind  StreamKind
	Delta string
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

// ToolResult is the trailing half of a tool entry: the tool's delivered result plus the deterministic compression metadata the TUI renders.
type ToolResult struct {
	Name         string
	Result       string
	BytesDropped int
	Lines        int
	Dropped      int
	Compressed   bool
}

// Event is one merged observation from the engine seam, carrying exactly one of
// Stream (a reasoning/answer delta) or Tool (a tool start/result). The app
// pushes these onto a single ordered feed in the order the engine emits them,
// so the turn event timeline records the model's true arrival order instead of
// the completion order of Bubble Tea's parallel wait commands (tea.Batch makes
// no ordering guarantee).
type Event struct {
	RunID     int
	TurnStart bool
	Stream    *StreamUpdate
	Tool      *ToolUpdate
}

// EventFeed bridges the engine's live event stream into the TUI's rendering
// loop on one FIFO channel shared by stream deltas and tool observations.
type EventFeed struct {
	updates chan Event
}

// NewEventFeed builds a live merged feed ready to be handed to a Model via
// Dependencies.
func NewEventFeed() *EventFeed {
	return &EventFeed{updates: make(chan Event, 256)}
}

// Drain discards queued observations before a new UI turn starts, preventing an
// old turn's backlog from attaching to the next prompt.
func (f *EventFeed) Drain() {
	for {
		select {
		case <-f.updates:
		default:
			return
		}
	}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (f *EventFeed) UpdateChan() chan<- Event { return f.updates }

// Updates exposes the same feed for reading (tests/observation).
func (f *EventFeed) Updates() <-chan Event { return f.updates }

// eventMsg carries one merged event from the engine seam into the UI loop.
type eventMsg struct {
	update Event
}

// eventWait returns a command that blocks until the next merged event arrives
// on the engine-seam channel, then delivers it to the UI loop as an eventMsg.
func eventWait(f *EventFeed) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-f.updates
		if !ok {
			return nil
		}
		return eventMsg{update: u}
	}
}
