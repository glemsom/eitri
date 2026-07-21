# HTMX + Templ shell with browser islands

**Status**: Accepted

## Context

Eitri needs a browser UI while preserving Go as the primary implementation language and single-binary Linux deployment. Chat UX needs low-latency token feedback, rich rendered output (Markdown, code, Mermaid, KaTeX, diffs), and future terminal capability, but Eitri should avoid full SPA complexity and a separate frontend server.

## Decision

Adopt a server-rendered hypermedia shell with isolated browser islands.

- Go server owns application state, sessions, agent runs, routing, validation, security boundaries, and HTML rendering.
- Templ renders full pages, HTMX fragments, and rich UI components.
- HTMX handles standard request/response interactions: forms, navigation, partial swaps, out-of-band swaps, indicators.
- SSE remains the default assistant-run streaming transport. Packets use a structured JSON envelope.
- `eitri-stream` browser island owns `EventSource` lifecycle, token buffering, run status, reconnect UI, and final Markdown render call.
- Other islands handle browser-native local behavior such as copy buttons, line-wrap toggles, Mermaid initialization, diff toggles, and future terminal rendering.
- No frontend framework, no SPA global store, no npm/bundler, and no separate frontend server.
- WebSocket is reserved for true bidirectional PTY/terminal use, not chat.

## Considered Options

- **Embedded SPA**: Vanilla HTML + JS or a frontend framework embedded via `go:embed`. Easier JSON API mental model but introduces client-side application state, more JavaScript, framework/bundler pressure, and split ownership of validation/rendering.
- **Pure HTMX + Templ**: Server owns all rendering and DOM is state. Simple and Go-first, but awkward for low-latency token flushing, rich library lifecycles, copy buttons, diff toggles, and future terminal interactions.
- **HTMX + Templ shell with browser islands**: Server remains authoritative while small, bounded JS islands handle browser strengths and fast feedback.

## Consequences

Positive:

- Keeps Go and Templ as authoritative UI implementation.
- Avoids SPA/global store complexity.
- Fits structured SSE event stream better than HTMX SSE auto-swap.
- Allows rich browser features (Mermaid, KaTeX, Prism, Clipboard, View Transitions, future xterm.js) without moving app to SPA.
- Keeps chat protocol simple with SSE request/response rendering endpoints.
- Maintains single-binary deployment path by embedding pinned vendor assets for v1 hardening.

Negative / tradeoffs:

- More JavaScript than pure HTMX, but bounded by island contracts and browser E2E tests.
- Island lifecycle must be idempotent across HTMX swaps.
- Optional library failures need graceful fallback code.
- `templ generate` remains required after template changes.

Constraints:

- Browser islands never own canonical application state.
- Islands must not use `innerHTML` with untrusted LLM/user/token data.
- WebSocket may be added only for a future interactive terminal/PTY route.
