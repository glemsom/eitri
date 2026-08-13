package tui

import (
	tea "github.com/charmbracelet/bubbletea"
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

// StreamUpdate is one additive answer-text delta for the transcript. It mirrors
// the engine seam's StreamEvent{Kind: AnswerStream} while keeping the TUI
// decoupled from the engine package.
type StreamUpdate struct {
	Delta string
}

// NewStreamer builds a live answer stream ready to be handed to a Model via
// Dependencies. The caller wires the engine event seam's AnswerStream deltas
// into UpdateChan.
func NewStreamer() *Streamer {
	return &Streamer{updates: make(chan StreamUpdate, 256)}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (s *Streamer) UpdateChan() chan<- StreamUpdate { return s.updates }

// Updates exposes the same feed for reading (tests/observation).
func (s *Streamer) Updates() <-chan StreamUpdate { return s.updates }

// answerDeltaMsg carries one streamed answer-text delta into the UI loop. It is
// produced by the waiting command (streamWait) so the in-progress answer grows
// incrementally, even with no keyboard input.
type answerDeltaMsg struct {
	delta string
}

// streamWait returns a command that blocks until the next streamed answer delta
// arrives on the engine-seam channel, then delivers it to the UI loop as an
// answerDeltaMsg. The model re-issues it after each delta so the in-progress
// answer keeps growing (issue #83). When the channel closes it returns nil so
// the polling stops.
func streamWait(s *Streamer) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-s.updates
		if !ok {
			return nil
		}
		return answerDeltaMsg{delta: u.Delta}
	}
}
