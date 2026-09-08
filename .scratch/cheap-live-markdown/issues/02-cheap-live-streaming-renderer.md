# 02: Cheap renderer for live streaming thinking/answer blocks

**What to build:** During a live (in-progress) turn, render the streaming thinking and answer blocks with a lightweight ANSI word-wrap instead of the full glamour+goldmark pipeline, so each streaming delta renders in tens of microseconds instead of ~19ms with ~666k allocations. Committed turns keep the full glamour markdown render unchanged.

**Blocked by:** 01 (benchmark baseline to measure against).

**Status:** ready-for-agent

- [ ] Streaming reasoning/answer panes render their body via a cheap ANSI wrap; committed, error, and stopped panes still go through glamour (no divergence in committed output)
- [ ] The streaming block still shows the bordered/colored/italic pane and thinking header (effort line) just as today — only the body's markdown emphasis is simplified while streaming
- [ ] The existing content assertions in the model/stream tests (`ansiStrip` contains "first reasoning", etc.) still pass
- [ ] The live-think benchmark from 01 shows a large ms/allocs reduction (order of magnitude+) per render

Considerations to verify while implementing:
- The cheap wrap hard-wraps at the pane width; decide how it handles long unbroken tokens (e.g. URLs/code runs) so it never crashes or renders an overlarge line.
- The live body must remain byte-compatible decision-wise with the windowing already applied by `liveStreamingText` (8KiB tail window) so a long stream stays bounded.