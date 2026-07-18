# Context Window Utilization Panel

## Problem Statement

Users of Eitri have no visibility into how much of the LLM's context window is being consumed during a chat session. Context window overflows cause silent truncation or errors ("Context length exceeded"), but the user only discovers this when it breaks. Without a live indicator, users cannot gauge whether a session is approaching its limit, adjust message length, or decide when to start a fresh session. The existing `TokenUsage` footer below each message is per-response, not cumulative, and offers no visual indicator of proximity to the context window boundary.

## Solution

Add a 4th sidebar section titled "Context" between the Tools and Thinking panels. This section shows a live, per-turn progress bar of total tokens used vs the configured context window. In its compact state it displays a single bar. Clicking the bar expands to a per-category breakdown (system prompt, history, skills, completion) with progress bars for each. The panel updates automatically via a new SSE event type broadcast after each agent turn, and shows "No active run" when idle.

## User Stories

1. As a user, I want to see a progress bar of total context tokens used vs my model's context window, so that I know at a glance how close I am to the limit.
2. As a user, I want this display to update live during a run (after each LLM turn), so that I can watch context consumption grow in real-time.
3. As a user, I want to click the compact panel to expand per-category breakdown (system prompt, conversation history, active skills, completion), so that I understand where tokens are being spent.
4. As a user, I want the panel to display "No active run" when idle, so that I know the panel's state at a glance without confusion.
5. As a user, I want the context window value to match what I configured in Settings (`context_window_tokens`, default 256K), so that estimates are calibrated to my model.
6. As a user, I want the percentage indicator alongside the raw numbers, so that I can quickly assess headroom without mental arithmetic.
7. As a developer, I want the server-side token estimation to be a pure function, so that it is easy to unit test and reason about.

## Implementation Decisions

### Architecture

- **New SSE event type**: `context_update` broadcast on the existing SSE stream after each agent turn (after tool results are appended to conversation history), plus one final broadcast after the `done` event. The event is broadcast via `Writer.State().Broadcast()` — no new SSE endpoint needed.

- **New struct `ContextUpdate`** in `runstate` package:
  ```go
  type ContextUpdate struct {
      TotalTokens      int `json:"total_tokens"`
      ContextWindow    int `json:"context_window"`
      PromptTokens     int `json:"prompt_tokens"`
      CompletionTokens int `json:"completion_tokens"`
      SystemTokens     int `json:"system_tokens"`
      HistoryTokens    int `json:"history_tokens"`
      SkillTokens      int `json:"skill_tokens"`
  }
  ```
  All fields are always stamped in every `context_update` event.

- **New pure function `ComputeContext`** in `runstate` package:
  ```go
  type ComputeContextInput struct {
      Messages      []litellm.Message
      ContextWindow int
  }
  func ComputeContext(in ComputeContextInput) *ContextUpdate
  ```
  Estimates tokens from message content using the same `4 chars/token` heuristic as `EstimateUsage`. System prompt identified by `role == "system"`. Skill content is the portion of system prompt after the last "Activated skill" prefix (tracked separately in the agent loop).

- **Broadcast point**: At the bottom of the agent turn loop in `runner/loop.go`, after all tool results are appended to history but before the `}` closing the `for turn` block. Also broadcasted on the final turn (before `return nil`) and in the max-turns-exceeded path (before `return`).

### UI

- **DOM**: Static `<eitri-context>` custom element in `base.templ` sidebar, with `data-context-window` attribute. The 4th sidebar section, between `#tool-activity` and `#thinking-panel`.

- **Compact state**: Single line showing total progress bar + `12,847 / 128,000 (10%)`.

- **Expanded state** (toggle by clicking the bar):
  ```
  ▾ Context
     Total        ████████████░░░  12,847 / 128K (10%)
     Prompt       ████████░░░░░░░       8,921
       System     ██░░░░░░░░░░░░░         823
       History    ██████░░░░░░░░░       7,423
       Skills     ░░░░░░░░░░░░░░░         675
     Completion   ████░░░░░░░░░░░       3,926
  ```

- **Idle state**: Shows "No active run" text, no bars.

- **Browser island**: `eitri-stream.js` handles `context_update` packets. New `<eitri-context>` island JS handles expand/collapse toggle.

### Server-side changes

- **`runstate/runstate.go`**: Add `ContextUpdate` struct, `ComputeContext` function, `ContextUpdateEvent()` method on `Writer`.
- **`runner/loop.go`**: Import `ComputeContext`, call after each turn and broadcast via SSE. The `RunAgent` function signature needs `contextWindow int` parameter (or a callback to retrieve it).
- **`runner/service.go`**: Pass context window from config through to `RunAgent`.
- **`api/templates/base.templ`**: Add 4th sidebar section with `<eitri-context>` element.
- **`api/assets/eitri-stream.js`**: Add `context_update` event handler.

## Testing Decisions

### Testing philosophy
- Test external behavior: the broadcast of `context_update` events with correct values.
- Pure functions (`ComputeContext`) get table-driven unit tests.
- Integration tests verify that `RunAgent` broadcasts `context_update` at expected points.

### Modules tested

1. **`internal/runstate`** — new tests for `ComputeContext`:
   - Empty input
   - Only system prompt
   - System + single user/assistant exchange
   - System + history + skills (simulated)
   - Very large context (stress the 4-char heuristic)
   - Prior art: `TestWriter_ThinkingDelta` in `runstate_test.go`

2. **`internal/runner`** — new tests in `loop_test.go`:
   - Single turn, no tool calls → see `context_update` broadcast once (before done)
   - Multi-turn with tool calls → see `context_update` after each turn
   - Max turns exceeded → see final `context_update` before error
   - Cancelled run → no `context_update` (stream closed)
   - Prior art: `TestRunAgent_SingleTurn_NoToolCalls`, `TestRunAgent_MultiTurn_ToolCallThenResponse`

3. **No browser E2E tests** for this feature (DOM-only island, no new API endpoint).

## Out of Scope

- **Exact token counts from provider responses**: This feature uses `4 chars/token` estimation only. Provider-returned usage data (from OpenAI/Anthropic response headers) is not integrated. The `TokenUsage` from the `done` event remains the existing per-message estimate.
- **Context window negotiation**: No dynamic detection of the model's actual context window. The value comes from user-configured `context_window_tokens`.
- **Per-message contribution column**: The expanded view shows category aggregates, not per-message line items. That would require tracking individual message sizes.
- **Color-coded warnings** (yellow at 60%, red at 85%): Deferred to a future iteration.

## Further Notes

- The `4 chars/token` heuristic is the same one used by `EstimateUsage` in `runstate.go`. It is a rough estimate, especially for code-heavy conversations with many symbols. Users should treat the numbers as directional.
- Future work: integrate real token counts from the litellm response metadata when providers return them. This would make the meter precise.
- The `context_window` config setting (default 256K) is user-adjustable in Settings. Users with 128K or 200K models should set it accordingly.
