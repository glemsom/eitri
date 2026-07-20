# 01 — Thread toolCallKey through SSE state for unambiguous card matching

**What to build:** The JS-side `tool_call` handler generates a unique `toolCallKey` for each tool entry, but the `tool_result` and `component` SSE events carry no way to map back to that key. `renderToolCard` guesses via `querySelector('.tool-running')` and `renderComponent` via `allWrappers[last]` — both fail under concurrent tool calls.

Save `lastToolCallKey` in `createStreamState` when handling `tool_call`. Pass it by name to `renderToolCard(sessionId, 'tool_result', packet, toolCallKey)` and `renderComponent(sessionId, packet, toolCallKey)`. Inside those functions, use `data-tool-key` attribute lookup (`details[data-tool-key="X"]`) instead of heuristic DOM queries.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Add `lastToolCallKey` field to `createStreamState()`, set on `tool_call`, consumed on `tool_result` and `component`
- [ ] `renderToolCard` takes explicit `toolCallKey` param, uses `details[data-tool-key="X"]` instead of `.tool-running`/last-fallback
- [ ] `renderComponent('FileEditCard')` takes explicit `toolCallKey`, uses `details[data-tool-key="X"]` instead of `allWrappers[last]`
- [ ] Remove `.tool-running` heuristic from `renderToolCard` — dead path after above
