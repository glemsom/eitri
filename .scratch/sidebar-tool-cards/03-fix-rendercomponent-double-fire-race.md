# 03 — `renderComponent('FileEditCard')` double-fires via setTimeout, races with `renderToolCard`

**What to build:** `renderComponent('FileEditCard')` calls `setTimeout(doRender, 0)` and `setTimeout(doRender, 100)` as a "retry in case HTMX hasn't finished" fallback. Both calls fire independent `htmx.ajax()` POST requests targeting the same wrapper with `swap: 'innerHTML'`.

This creates two problems:

1. **Status overwrite race:** If `renderToolCard` (from `tool_result` SSE) updates the outer `<summary>` to "done"/"error" between tick 0 and tick 100, the second `renderComponent` HTMX response arrives and **replaces** the entire wrapper content (including the updated summary). The entry reverts to whatever the server returned (FSM data), losing the done/error status and elapsed time.

2. **DOM thrash:** Two HTMX swaps on the same element in rapid succession. First response renders, second overwrites. Causes unnecessary flash/reflow and potential HTMX internal state confusion.

Fix: Remove the double-fire. If the first attempt fails (wrapper not found), schedule exactly one retry with a guard that checks whether `renderToolCard` has already applied its updates. Or better: serialize the `component` HTML and `tool_result` HTML into a single render call, or ensure `renderComponent` fires after `renderToolCard` and skips if already done.

Simplest correct fix: Store a `pendingComponentRender` flag on the tool wrapper. `renderComponent` sets it and fires one attempt. `renderToolCard` checks it and if set, defers its own status update to after the component render completes. Or vice versa: `renderComponent` checks if `renderToolCard` already ran (`.tool-done`/`.tool-error` present) and if so, skips.

**Blocked by:** 01 (gives both functions the correct toolCallKey so they can coordinate on the same wrapper)
02 (nested `<details>` fix removes the structural overwrite — but the timing race remains)

**Status:** ready-for-agent

- [ ] Remove double `setTimeout(doRender, N)` — pick single schedule strategy
- [ ] Add coordination guard so `renderComponent` and `renderToolCard` don't step on each other
- [ ] Verify sidebar shows correct status (done/error icon, elapsed time) after edit tool completes
- [ ] Verify no redundant HTMX calls in browser DevTools network tab
