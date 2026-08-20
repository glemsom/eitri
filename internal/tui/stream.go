package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Streamer bridges the engine's live answer-text event stream into the TUI's rendering loop .
type Streamer struct {
	updates chan StreamUpdate
}

// StreamKind identifies which stream a StreamUpdate carries: chain-of-thought reasoning or assistant answer text.
type StreamKind int

const (
	ReasoningStream StreamKind = iota
	AnswerStream
)

// StreamUpdate is one additive delta for the transcript, on one of the two channels the engine streams: reasoning or answer text.
type StreamUpdate struct {
	Kind  StreamKind
	Delta string
}

// NewStreamer builds a live stream ready to be handed to a Model via Dependencies.
func NewStreamer() *Streamer {
	return &Streamer{updates: make(chan StreamUpdate, 256)}
}

// UpdateChan exposes the feed the app wires the engine listener to.
func (s *Streamer) UpdateChan() chan<- StreamUpdate { return s.updates }

// Updates exposes the same feed for reading (tests/observation).
func (s *Streamer) Updates() <-chan StreamUpdate { return s.updates }

// streamDeltaMsg carries one streamed delta (reasoning or answer text) into the UI loop.
type streamDeltaMsg struct {
	kind  StreamKind
	delta string
}

// streamWait returns a command that blocks until the next streamed delta arrives on the engine-seam channel, then delivers it to the UI loop as a streamDeltaMsg.
func streamWait(s *Streamer) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-s.updates
		if !ok {
			return nil
		}
		return streamDeltaMsg{kind: u.Kind, delta: u.Delta}
	}
}
