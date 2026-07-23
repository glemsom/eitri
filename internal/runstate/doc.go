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
// ComputeContext estimates token counts for the current conversation using a
// 4-char-per-token heuristic, broken down by category (system, skill, history).
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
//   - internal/litellm — litellm.Message type for context computation
//
// Extension points:
//   - Add new SSE event types by adding fields to SSEEvent or creating new
//     Writer helper methods
//   - Replace token estimation heuristic with actual model tokenizer
//   - Add subscriber metrics (count, replay count) for debugging
package runstate
