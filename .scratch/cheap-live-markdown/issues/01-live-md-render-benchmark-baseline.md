# 01: Live-thinking render benchmark baseline

**What to build:** A repeatable benchmark that measures the per-frame cost of rendering a live streaming thinking block from a realistic long chain-of-thought (mixed prose, code fences, lists, punctuation), through the real `liveMarkdownCache → RenderMarkdown` path. This captures today's glamour baseline (~19ms, ~666k allocs per 9KiB render) so the follow-up cheap-renderer work can be measured against a fixed number and regressions can't slip in silently.

**Blocked by:** None (can start immediately).

**Status:** done

- [x] `BenchmarkLiveThinkingRender` exists under `internal/tui`, renders a realistic mixed-prose/code reasoning blob through the production render path, and reports ns/op, B/op, allocs/op
- [x] The benchmark pre-builds the blob outside the timed loop so it measures the render, not fixture construction
- [x] It runs against the unfixed glamour path and is committed passing (documents the current baseline)  →  DONE: baseline ~6.78ms/op, ~298k allocs/op per 9KiB live thinking render (`internal/tui/live_think_render_bench_test.go`, `BenchmarkLiveThinkingRender`)