# 0014 — Live context window utilization panel

Context window overflows cause silent truncation or errors in Eitri sessions, but users have no visibility into how much of the window is consumed. The existing per-message token-usage footer is not cumulative and offers no visual proximity indicator.

We add a 4th sidebar section ("Context") that shows a live progress bar of total estimated tokens vs the configured context window. A new SSE event type (`context_update`) is broadcast after each agent turn carrying per-category token estimates. The panel is a static `<eitri-context>` custom element; clicking it toggles a per-category breakdown (system prompt, history, skills, completion).

Token estimation uses the same `4 chars/token` heuristic as the existing `EstimateUsage` function. A pure `ComputeContext` function in the `runstate` package accepts a message slice and the configured context window, making it unit-testable without session manager dependencies.

We opted for server-side estimation (not provider-returned usage) because it is available immediately on every turn and does not depend on all providers returning usage metadata in their streaming responses. Provider-returned counts can be integrated later for precision.
