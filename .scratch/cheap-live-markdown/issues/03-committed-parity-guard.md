# 03: Committed-parity and liveness regression guard

**What to build:** A regression guard that proves the cheap live renderer never leaks into committed output and does not break the product's "short reasoning renders each delta immediately" guarantee. The committed turn must still render full glamour markdown byte-identically to before; a live turn must still show every reasoning/answer delta as it arrives.

**Blocked by:** 02 (the cheap live renderer).

**Status:** ready-for-agent

- [ ] A test renders the same reasoning as a committed block and asserts the committed pane still passes through glamour (markdown styling present), regardless of the cheap live path
- [ ] The existing streaming-liveness tests (`stream_test.go`: deltas appear immediately, back-to-back, no injected clock) still pass after the renderer swap
- [ ] A byte-parity check: committed output with the change equals committed output from before the change for a fixed reasoning sample
- [ ] The busy-render and unmarshalled-output tests in `internal/tui` still pass