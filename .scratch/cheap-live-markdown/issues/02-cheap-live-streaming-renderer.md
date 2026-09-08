# 02: Cheap renderer for live streaming thinking/answer blocks

**What to build:** During a live (in-progress) turn, render the streaming thinking and answer blocks with a lightweight ANSI word-wrap instead of the full glamour+goldmark pipeline, so each streaming delta renders in tens of microseconds instead of ~19ms with ~666k allocations. Committed turns keep the full glamour markdown render unchanged.

**Blocked by:** 01 (benchmark baseline to measure against).

**Status:** done

- [x] Streaming reasoning/answer panes render their body via a cheap ANSI wrap; committed, error, and stopped panes still go through glamour (no divergence in committed output)
- [x] The streaming block still shows the bordered/colored/italic pane and thinking header (effort line) just as today — only the body's markdown emphasis is simplified while streaming
- [x] The existing content assertions in the model/stream tests (`ansiStrip` contains "first reasoning", etc.) still pass
- [x] The live-think benchmark from 01 shows a large ms/allocs reduction (order of magnitude+) per render  →  `internal/tui/cheap_live_render.go`; baseline 6.93ms/op ~298k allocs → cheap 0.758ms/op 3,895 allocs per 9KiB live thinking render (~9.1× time, ~76× allocs)
