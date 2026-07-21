# ADR 0004: Merge tool activity into inline tool cards

Replace the separate activity panel with live tool cards in the message stream. Tool cards appear immediately on `tool_call` (running state) and morph to done on `tool_result`, eliminating the duplicate parallel representation of tool execution.

**Status**: Accepted

## Context

The current UX has two parallel representations of tool execution:

1. **Activity panel** (`<details#activity-panel>`): a collapsible bar above the message stream listing `Started`/`Finished` entries with raw JSON detail.
2. **Tool cards** (`<div.tool-call-container>`): inline in the message stream with structured status, formatted output, copy/wrap buttons.

Problems identified:

- Activity panel is open by default, filling vertical space
- `Started`/`Finished` entries duplicate what the inline tool card already shows
- Raw JSON in activity entries is useless for human reading vs the formatted tool card
- Activity entries are decoupled from the tool card — user cannot tell which entry maps to which card
- Over long sessions, the panel fills with noise

## Decision

Remove the separate activity panel entirely. Surface tool status live inside the tool card in the message stream:

- When the LLM emits a `tool_call` SSE event, a tool card appears immediately in the message stream in **running** state, showing tool name + arguments.
- When the `tool_result` event arrives, the same card morphs to **done** state, showing the output.
- The card lifecycle (running → done) replaces the `Started`/`Finished` activity entries.
- The `run-status` indicator (Idle/Streaming/Tool running/Done) remains as a lightweight status bar.

## Considered Options

- **Keep activity panel as collapsed-by-default sidebar**: Adds complexity, still duplicates info. Rejected.
- **Keep activity panel as opt-in toggle**: Adds maintenance burden for marginal value. Rejected.
- **Merge into tool card (chosen)**: Eliminates duplication, couples status with content, reduces vertical space.

## Consequences

Positive:

- No more separate activity panel = less visual clutter
- Tool intent ("LLM wants to run ls -la") visible immediately in the card
- Card animates naturally from running to done, single source of truth
- Long sessions don't accumulate noise in a separate panel

Negative:

- Loss of chronological tool log separate from chat (can be re-added later as collapsed sidebar if needed)
- JS needs to inject "running" card on `tool_call` event (currently only injects on `tool_result`)

## Implementation plan

1. Expose `tool_call` data to the render endpoint so JS can inject running cards
2. JS: inject running tool card on `tool_call` SSE event
3. JS: morph running card to done on `tool_result` event
4. Remove activity panel template + CSS + JS
5. Merge ToolCallCard + ToolResultCard templates into unified ToolCard
6. Add live elapsed timer on running cards
