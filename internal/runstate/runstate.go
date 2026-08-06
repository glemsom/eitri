// Package runstate provides SSE event broadcast infrastructure for active agent runs.
// It owns subscriber fan-out, event history, and typed SSE event writing.
// It is network-agnostic — it manages channels, not HTTP connections.
package runstate

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/tokenizer"
)

// RenderKind maps SSE events to the render kind the browser island should POST.
type RenderKind string

const (
	RenderKindToolCard  RenderKind = "tool_card"
	RenderKindComponent RenderKind = "component"
	RenderKindError     RenderKind = "error"
	RenderKindMarkdown  RenderKind = "markdown"
)

// SSEEvent represents one SSE data packet sent to the browser.
type SSEEvent struct {
	Type      string                `json:"type"`
	Kind      RenderKind            `json:"kind,omitempty"`
	Content   string                `json:"content,omitempty"`
	Name      string                `json:"name,omitempty"`
	Tool      string                `json:"tool,omitempty"`
	Args      any                   `json:"args,omitempty"`
	Output    any                   `json:"output,omitempty"`
	Data      any                   `json:"data,omitempty"`
	Message   string                `json:"message,omitempty"`
	MessageID string                `json:"message_id,omitempty"`
	Usage     *tokenizer.TokenUsage `json:"usage,omitempty"`
	Timestamp time.Time             `json:"timestamp,omitempty"`
	Turn      int                   `json:"turn,omitempty"`
	// Replayed marks an event delivered from run-state history to a subscriber
	// joining mid-run (or reconnecting), as opposed to a live broadcast. The
	// browser island skips replayed token/component events: committed messages
	// are already rendered by the server on page load, and re-accumulating
	// their tokens into a fresh streaming bubble duplicates the message after a
	// session switch-back. The flag is set on the per-subscriber copy only —
	// the stored history and the persisted timeline keep Replayed=false.
	Replayed bool `json:"replayed,omitempty"`
}

// LLMCallInfo carries per-turn LLM call correlation data on the llm_call SSE
// event. It joins a turn to its HTTP trace by ID at write time (issue #988):
// TraceID is the recorder-assigned trace of the successful attempt, Attempt
// the zero-based attempt number of that call, Attempts the total number of
// attempts (initial + retries) for the turn, and the timing fields summarize
// the call (total duration, time-to-first-byte, time-to-first-token).
type LLMCallInfo struct {
	TraceID    string `json:"trace_id"`
	Attempt    int    `json:"attempt"`
	Attempts   int    `json:"attempts"`
	DurationMs int64  `json:"duration_ms"`
	TTFBMs     int64  `json:"ttfb_ms"`
	TTFTMs     int64  `json:"ttft_ms"`
}

// Token batching and history bounds. High-volume stream content (token and
// thinking_delta deltas) is accumulated server-side and flushed as a single
// SSE event on a short interval or character budget, so the client receives
// the same complete text with far fewer network frames.
const (
	// tokenFlushInterval is the maximum time batched token/thinking_delta
	// content is held before being flushed to subscribers as one event.
	tokenFlushInterval = 50 * time.Millisecond
	// tokenFlushBudget is the character budget at which a pending batch is
	// flushed immediately, even before the interval elapses.
	tokenFlushBudget = 4096
	// maxHistoryEvents caps the number of events retained in run-state history.
	maxHistoryEvents = 4096
	// maxHistoryBytes caps the total content bytes of high-volume (token and
	// thinking_delta) events retained in run-state history. Keeps replay and
	// diagnostics memory-bounded for long reasoning streams while preserving
	// all semantic events (tool calls, results, context updates).
	maxHistoryBytes = 1 << 20 // 1 MiB
)

// tokenBatch accumulates consecutive same-type stream content (token or
// thinking_delta) until a flush interval, character budget, or an interleaving
// non-batched event forces delivery.
type tokenBatch struct {
	typ     string
	content strings.Builder
	turn    int
}

// State tracks one active assistant run per session.
// Owns subscriber fan-out, event history, and text buffer.
type State struct {
	mu sync.Mutex

	subscribers     map[uint64]*subscriber
	nextSubscriber  uint64
	streamsClosed   bool
	history         []SSEEvent
	historyBytes    int
	subscriberCount uint64
	replayCount     uint64

	bufferMu sync.Mutex
	buffer   strings.Builder

	reasoningMu sync.Mutex
	reasoning   strings.Builder

	// Token/thinking_delta batching. The pending batch and flush timer are
	// guarded by mu (the same lock that protects subscribers/history), so a
	// batch flush and an interleaving Broadcast are atomic relative to each
	// other and event ordering is exact.
	batch      *tokenBatch
	batchTimer *time.Timer
}

// New creates a new State ready for use.
func New() *State {
	return &State{
		subscribers: make(map[uint64]*subscriber),
	}
}

// subscriber owns one SSE output channel. Its mutex serializes concurrent
// send and close so no goroutine ever writes to a channel that another
// goroutine is closing (or vice versa).
type subscriber struct {
	mu     sync.Mutex
	closed bool
	ch     chan SSEEvent
}

// send performs a non-blocking send. Safe to call concurrently with close.
func (sub *subscriber) send(evt SSEEvent) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	select {
	case sub.ch <- evt:
	default:
	}
}

// close marks the subscriber as closed and closes its channel. Safe to call
// concurrently with send; whichever goroutine wins the lock first performs
// the close and the other becomes a no-op.
func (sub *subscriber) close() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	close(sub.ch)
}

// BufferString returns accumulated text.
func (s *State) BufferString() string {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return s.buffer.String()
}

// AppendBuffer appends text to accumulator.
func (s *State) AppendBuffer(text string) {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	s.buffer.WriteString(text)
}

// ReasoningBufferString returns accumulated reasoning text.
func (s *State) ReasoningBufferString() string {
	s.reasoningMu.Lock()
	defer s.reasoningMu.Unlock()
	return s.reasoning.String()
}

// AppendReasoningBuffer appends reasoning text to accumulator.
func (s *State) AppendReasoningBuffer(text string) {
	s.reasoningMu.Lock()
	defer s.reasoningMu.Unlock()
	s.reasoning.WriteString(text)
}

// Subscribe allocates a subscriber channel and replays history.
// Returns subscriberID, receive-only channel, and whether the stream is still open.
// If streams are already closed, the channel carries history (if any) and is closed.
//
// Any pending batched token/thinking_delta content is flushed first so a late
// subscriber receives the most recent text before live events.
func (s *State) Subscribe() (uint64, <-chan SSEEvent, bool) {
	s.flushBatch()

	s.mu.Lock()

	history := append([]SSEEvent(nil), s.history...)

	if s.streamsClosed {
		s.mu.Unlock()
		ch := make(chan SSEEvent, 512)
		for _, evt := range history {
			ev := evt
			ev.Replayed = true
			ch <- ev
		}
		close(ch)
		return 0, ch, len(history) > 0
	}

	id := s.nextSubscriber
	s.nextSubscriber++

	// Size the channel to fit the full history so that replaying
	// history below never blocks (history can grow beyond 512).
	bufSize := 512
	if len(history) > bufSize {
		bufSize = len(history)
	}
	sub := &subscriber{ch: make(chan SSEEvent, bufSize)}
	s.subscribers[id] = sub
	s.subscriberCount++
	if len(history) > 0 {
		s.replayCount++
	}

	s.mu.Unlock()

	// Replay history outside the lock so Subscribe cannot deadlock
	// when history exceeds the default channel buffer. sub.send is
	// safe to call concurrently with sub.close from another goroutine.
	// Each replayed event is marked Replayed on its per-subscriber copy so
	// the browser island can distinguish history (already rendered on page
	// load / already accumulated in the streaming bubble) from live events
	// and skip re-accumulating token content (duplicate messages after a
	// session switch-back).
	for _, evt := range history {
		ev := evt
		ev.Replayed = true
		sub.send(ev)
	}

	return id, sub.ch, true
}

// Unsubscribe removes a subscriber and closes its channel.
func (s *State) Unsubscribe(id uint64) {
	s.mu.Lock()
	sub, ok := s.subscribers[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.subscribers, id)
	s.mu.Unlock()

	sub.close()
}

// Broadcast sends an event to all current subscribers and appends it to history.
// No-op after CloseStreams is called.
//
// Token and thinking_delta events normally flow through Writer.Token and
// Writer.ThinkingDelta, which batch them server-side. A direct Broadcast of
// those types is delivered immediately. Any other event type first flushes
// pending batched content under the same lock, so event ordering between
// batched stream text and interleaving events is exact.
func (s *State) Broadcast(evt SSEEvent) {
	s.mu.Lock()
	if evt.Type != "token" && evt.Type != "thinking_delta" {
		s.flushBatchLocked()
	}
	s.broadcastLocked(evt)
	s.mu.Unlock()
}

// broadcastLocked appends evt to history and sends it to all current
// subscribers. Must be called with s.mu held.
func (s *State) broadcastLocked(evt SSEEvent) {
	if s.streamsClosed {
		return
	}
	evt.Timestamp = time.Now()
	s.appendHistory(evt)
	subscribers := make([]*subscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		subscribers = append(subscribers, sub)
	}
	for _, sub := range subscribers {
		sub.send(evt)
	}
}

// flushBatchLocked flushes any pending batched content as part of the current
// operation, so the batched text is appended before an interleaving event.
// Must be called with s.mu held.
func (s *State) flushBatchLocked() {
	b := s.swapBatchLocked()
	if b == nil {
		return
	}
	s.broadcastLocked(SSEEvent{Type: b.typ, Content: b.content.String(), Turn: b.turn})
}

// Flush delivers any pending batched token/thinking_delta content to
// subscribers and history immediately. Idempotent.
func (s *State) Flush() {
	s.flushBatch()
}

// addBatch accumulates token (or thinking_delta) content for batched delivery.
// A pending batch is flushed early when the character budget is reached, when
// the event type or turn changes, or by the flush timer.
func (s *State) addBatch(typ, content string, turn int) {
	if content == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A type or turn change flushes the pending batch first so events keep
	// their original order and carry the correct turn number.
	if s.batch != nil && (s.batch.typ != typ || s.batch.turn != turn) {
		s.flushBatchLocked()
	}
	if s.batch == nil {
		s.batch = &tokenBatch{typ: typ, turn: turn}
	}
	s.batch.content.WriteString(content)
	if s.batch.content.Len() >= tokenFlushBudget {
		s.flushBatchLocked()
		return
	}
	if s.batchTimer == nil {
		s.batchTimer = time.AfterFunc(tokenFlushInterval, s.flushTimerTick)
	}
}

// flushTimerTick is the timer callback for batched delivery.
func (s *State) flushTimerTick() {
	s.flushBatch()
}

// swapBatchLocked removes and returns the pending batch, stopping its timer.
// Must be called with s.mu held.
func (s *State) swapBatchLocked() *tokenBatch {
	b := s.batch
	s.batch = nil
	if s.batchTimer != nil {
		s.batchTimer.Stop()
		s.batchTimer = nil
	}
	return b
}

// flushBatch swaps out any pending batch and broadcasts it. Safe to call from
// Subscribe, History, the flush timer, and closeStreams. Broadcast uses
// flushBatchLocked instead so ordering with interleaving events is exact.
func (s *State) flushBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushBatchLocked()
}

// BroadcastDone sends a done event with kind "markdown", closes all subscriber streams, and marks the state as closed.
func (s *State) BroadcastDone(messageID string, usage *tokenizer.TokenUsage) {
	s.closeStreams(&SSEEvent{Type: "done", Kind: RenderKindMarkdown, MessageID: messageID, Usage: usage})
}

// BroadcastError sends an error event with kind "error" and closes all subscriber streams.
func (s *State) BroadcastError(msg string) {
	s.closeStreams(&SSEEvent{Type: "error", Kind: RenderKindError, Message: msg})
}

// BroadcastClosed sends a closed event and closes all subscriber streams.
func (s *State) BroadcastClosed(message string) {
	s.closeStreams(&SSEEvent{Type: "closed", Message: message})
}

// Closed returns true if the state streams have been closed.
func (s *State) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamsClosed
}

// History returns a copy of all broadcast events. Pending batched
// token/thinking_delta content is flushed first so the returned history is
// complete up to the call.
func (s *State) History() []SSEEvent {
	s.flushBatch()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SSEEvent(nil), s.history...)
}

// appendHistory appends an event to the run-state history and enforces the
// history bounds (event count and high-volume content byte budget). Must be
// called with s.mu held.
func (s *State) appendHistory(evt SSEEvent) {
	s.history = append(s.history, evt)
	if isHighVolumeEvent(evt) {
		s.historyBytes += len(evt.Content)
	}
	s.trimHistory()
}

// trimHistory enforces the run-state history bounds: a maximum event count and
// a maximum total byte budget for high-volume token/thinking content. Oldest
// events are dropped first, so replay-on-reconnect delivers the recent tail
// while keeping memory bounded for long reasoning streams. Semantic events
// (tool calls, results, context updates) are preserved wherever possible.
func (s *State) trimHistory() {
	for len(s.history) > maxHistoryEvents {
		dropped := s.history[0]
		s.history = s.history[1:]
		if isHighVolumeEvent(dropped) {
			s.historyBytes -= len(dropped.Content)
		}
	}
	for s.historyBytes > maxHistoryBytes {
		idx := -1
		for i, evt := range s.history {
			if isHighVolumeEvent(evt) {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		dropped := s.history[idx]
		s.history = append(s.history[:idx], s.history[idx+1:]...)
		s.historyBytes -= len(dropped.Content)
	}
}

// isHighVolumeEvent reports whether an event type carries per-stream text that
// is batched server-side and bounded in history.
func isHighVolumeEvent(evt SSEEvent) bool {
	return evt.Type == "token" || evt.Type == "thinking_delta"
}

// SubscriberCount returns the number of distinct subscribers that have connected.
func (s *State) SubscriberCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subscriberCount
}

// ReplayCount returns the number of times history has been replayed to a subscriber.
func (s *State) ReplayCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replayCount
}

func (s *State) closeStreams(evt *SSEEvent) {
	// Flush any pending batched content so subscribers receive the final text
	// before the closing event and channel teardown.
	s.flushBatch()

	s.mu.Lock()
	if s.streamsClosed {
		s.mu.Unlock()
		return
	}
	if evt != nil {
		evt.Timestamp = time.Now()
		s.appendHistory(*evt)
	}
	s.streamsClosed = true
	subscribers := make([]*subscriber, 0, len(s.subscribers))
	for id, sub := range s.subscribers {
		subscribers = append(subscribers, sub)
		delete(s.subscribers, id)
	}
	s.mu.Unlock()

	for _, sub := range subscribers {
		if evt != nil {
			sub.send(*evt)
		}
		sub.close()
	}
}

// Writer provides typed helpers that broadcast SSE events to a State.
type Writer struct {
	state       *State
	currentTurn int
}

// NewWriter creates an SSE writer that broadcasts to state.
func NewWriter(state *State) *Writer {
	return &Writer{state: state}
}

// State returns the underlying state (for direct broadcast access).
func (w *Writer) State() *State {
	return w.state
}

// SetTurn sets the current turn number for events broadcast through this Writer.
func (w *Writer) SetTurn(turn int) {
	w.currentTurn = turn
}

// Token appends to the text buffer and batches a text token event for
// server-side delivery. The client receives the same complete text in far
// fewer network frames (see State.addBatch).
func (w *Writer) Token(content string) {
	w.state.AppendBuffer(content)
	w.state.addBatch("token", content, w.currentTurn)
}

// ToolCall sends a tool call event with kind "tool_card".
func (w *Writer) ToolCall(name string, args any) {
	w.state.Broadcast(SSEEvent{Type: "tool_call", Kind: RenderKindToolCard, Tool: name, Args: args, Turn: w.currentTurn})
}

// ToolResult sends a tool result event with kind "tool_card".
func (w *Writer) ToolResult(name string, output any) {
	outputStr := ""
	if s, ok := output.(string); ok {
		outputStr = s
	} else {
		if b, err := json.Marshal(output); err == nil {
			outputStr = string(b)
		}
	}
	w.state.Broadcast(SSEEvent{Type: "tool_result", Kind: RenderKindToolCard, Tool: name, Output: outputStr, Turn: w.currentTurn})
}

// Done sends a done event with optional token usage and closes streams.
func (w *Writer) Done(messageID string, usage *tokenizer.TokenUsage) {
	w.state.BroadcastDone(messageID, usage)
}

// Component sends a generative UI component event with kind "component".
func (w *Writer) Component(data any) {
	w.state.Broadcast(SSEEvent{Type: "component", Kind: RenderKindComponent, Data: data, Turn: w.currentTurn})
}

// ContextUpdate broadcasts a context_update SSE event with token estimates.
func (w *Writer) ContextUpdate(update *tokenizer.ContextUpdate) {
	w.state.Broadcast(SSEEvent{Type: "context_update", Data: update, Turn: w.currentTurn})
}

// LLMCall broadcasts an llm_call event carrying the HTTP trace ID recorded for
// this turn's LLM call and its retry/timing measurements. The event is
// consumed by the persisted timeline so session reports can join turns to
// traces by ID (issue #988).
func (w *Writer) LLMCall(info LLMCallInfo) {
	w.state.Broadcast(SSEEvent{Type: "llm_call", Data: &info, Turn: w.currentTurn})
}

// SkillActivated broadcasts a skill_activated event with the skill name.
func (w *Writer) SkillActivated(name string) {
	w.state.Broadcast(SSEEvent{Type: "skill_activated", Tool: name, Turn: w.currentTurn})
}

// Error sends an error event with kind "error" and closes streams.
func (w *Writer) Error(msg string) {
	w.state.BroadcastError(msg)
}

// ThinkingDelta appends to the reasoning buffer and batches a thinking_delta
// SSE event with reasoning content for server-side delivery.
func (w *Writer) ThinkingDelta(content string) {
	w.state.AppendReasoningBuffer(content)
	w.state.addBatch("thinking_delta", content, w.currentTurn)
}
