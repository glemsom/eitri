// Package runstate provides SSE event broadcast infrastructure for active
// agent runs. It owns subscriber fan-out, event history, typed SSE event
// writing via Writer, and context window estimation.
//
// It is network-agnostic — State manages Go channels, not HTTP connections.
// Each active run has one State instance. Subscribers register via Subscribe(),
// receive events via a chan SSEEvent, and unregister via Unsubscribe().
// When a run completes, BroadcastDone/Error/Closed closes all subscriber
// channels and marks the state as closed.
//
// Writer provides typed helpers (Token, ToolCall, ToolResult, Component, etc.)
// that compose SSEEvent structs and broadcast them via State.
//
// Token and ThinkingDelta are batched server-side: consecutive deltas
// accumulate and are flushed as a single event on a short interval (~50ms)
// or a character budget (4096 chars), so the client receives the same text
// with far fewer SSE frames. Batches flush early on type/turn changes,
// non-token events, Subscribe, and stream close, preserving event order.
//
// Run-state event history is bounded: a max event count and a max byte budget
// for high-volume token/thinking content. Oldest content is dropped first, so
// a subscriber connecting mid-run or on reconnect replays the recent tail
// instead of the entire run, keeping long streaming runs memory-bounded.
//
// ComputeContext estimates token counts for the current conversation using a
// configurable chars-per-token ratio (from CalibrationStore, default 4.0),
// broken down by category (system, skill, history).
//
// Key types:
//   - State — SSE subscriber fan-out and event history
//   - Writer — typed SSE event broadcaster, wraps a State
//   - SSEEvent — one SSE data packet sent to the browser
//   - TokenUsage — token counts for a completed run
//   - ContextUpdate — estimated token counts by category
//
// Key functions:
//   - New — create a new State
//   - NewWriter — create a Writer wrapping a State
//   - ComputeContext — estimate token counts from messages
//   - EstimateUsage — rough token estimate from text length
//   - FormatErrorMessage — convert provider errors to user-friendly messages
//   - MaxTurnsMessage — user-facing max-turns message
//
// Dependencies:
//   - internal/message — message.Message type for context computation
//
// Extension points:
//   - Add new SSE event types by adding fields to SSEEvent or creating new
//     Writer helper methods
//   - Replace token estimation heuristic with actual model tokenizer
//   - Add subscriber metrics (count, replay count) for debugging
package runstate
