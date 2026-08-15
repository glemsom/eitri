package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Streamer bridges the engine's live answer-text event stream into the TUI's
// rendering loop (issue #83). The app's engine listener writes each streamed
// AnswerStream delta here, and the model drains it on the UI goroutine via a
// waiting command, re-rendering the in-progress assistant message in place —
// so the user watches the Markdown render grow token by token rather than a
// one-shot dump on completion. It is read-only against the agent loop: writes
// are non-blocking so a busy run never stalls on the transcript pane.
type Streamer struct {
	// updates is the buffered feed the app's engine listener writes to; the
	// model drains it on the UI goroutine (streamWait -> answerDeltaMsg).
	updates chan StreamUpdate
}

// StreamKind identifies which stream a StreamUpdate carries: chain-of-thought
// reasoning or assistant answer text. The two are kept apart
// — reasoning is never merged into the answer — so the TUI can render the
// thinking stream as its own collapsible block (issue #85).
type StreamKind int

const (
	// ReasoningStream is a streamed reasoning_content delta.
	ReasoningStream StreamKind = iota
	// AnswerStream is a streamed assistant answer content delta.
	AnswerStream
)

// StreamUpdate is one additive delta for the transcript, on one of the two
// channels the engine streams: reasoning or answer text. It mirrors the engine
// seam's StreamEvent{Kind} while keeping the TUI decoupled from the engine
// package.
type StreamUpdate struct {
	// Kind distinguishes reasoning from answer text.
	Kind StreamKind
	// Delta is this chunk's streamed text.
	Delta string
}

// NewStreamer builds a live stream ready to be handed to a Model via
// Dependencies. The caller wires the engine event seam's AnswerStream and
// ReasoningStream deltas into UpdateChan.
func NewStreamer() *Streamer {
	return &Streamer{updates: make(chan StreamUpdate, 256)}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (s *Streamer) UpdateChan() chan<- StreamUpdate { return s.updates }

// Updates exposes the same feed for reading (tests/observation).
func (s *Streamer) Updates() <-chan StreamUpdate { return s.updates }

// streamDeltaMsg carries one streamed delta (reasoning or answer text) into the
// UI loop. It is produced by the waiting command (streamWait) so the in-progress
// answer and thinking stream grow incrementally, even with no keyboard input.
type streamDeltaMsg struct {
	kind  StreamKind
	delta string
}

// streamWait returns a command that blocks until the next streamed delta
// arrives on the engine-seam channel, then delivers it to the UI loop as a
// streamDeltaMsg. The model re-issues it after each delta so the in-progress
// streams keep growing (issue #83). When the channel closes it returns nil so
// the polling stops.
func streamWait(s *Streamer) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-s.updates
		if !ok {
			return nil
		}
		return streamDeltaMsg{kind: u.Kind, delta: u.Delta}
	}
}
