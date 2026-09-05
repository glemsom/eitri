# Eitri performance & memory investigation — 2026-09-05

Method: spawned four `eitri -b` subagents (deepseek-v4-flash via opencode-go) in
separate workspaces/data-dirs, each with `--pprof 127.0.0.1:606X --pprof-mutex
--pprof-block`, on heavy multi-`bash`-tool log-analysis tasks. Sampled heap,
CPU, and goroutines from the pprof servers while the agents streamed, and
cross-checked the render-path hotspots with the repo's own benchmarks.

## Fixed: completed-tool-entry render cache dropped on every tool Start

`internal/tui/toollog.go` `Apply` ran `l.entryCache.m = nil` on every tool
*Start*, "because a new entry shifts every later index". But `toolLog.entries`
is append-only inside a session — there is no removal or shrink — so indexes
never shift. The wholesale clear was unnecessary and it defeated the cache in
exactly the case it exists for: a tool-heavy live turn, where every committed
tool card re-paid lipgloss `Style.Render` on each ~80 ms busy frame.

Fixed: `Apply(Start)` now appends without touching the memo. The guard test
`TestLiveTurnToolCache_dropsOnMutation` (which encoded the wrong "indexes shift"
premise) was renamed to `TestLiveTurnToolCache_completionInvalidatesStartRetains`
and now asserts a new Start keeps a completed sibling's cached row.

Evidence for the fix (new isolated micro-benchmark
`BenchmarkToolEntryRender_CachedAcrossStarts`, which strips the whole-page
rebuild/copy that `BenchmarkLiveTurnTimeline` is dominated by):

| tools | before fix (re-render every frame) | after fix (cache hits) |
|-------|-------------------------------------|------------------------|
| 200   | per-entry lipgloss render/frame     | 200 × ~105 ns lookup, 0 allocs |

The reintroduced `m = nil` behavior breaks the benchmark; the fixed version
renders 200 committed cards as 200 cheap map lookups (19.7 µs / frame, 0 B/op).

## Caveat — measuring the tool cache needs an isolated benchmark

`BenchmarkLiveTurnTimeline` (events_200 → events_8000: 162µs/385KB →
5.19ms/19.7MB) does **not** isolate the tool-entry cache. pprof on it shows the
cost is dominated by allocation/GC from `flowRenderer.render` +
`Transcript.renderEventFlow` — rebuilding the O(events) `flowTool`/flow-item
slices and the whole `busyPrefix + tail` page every frame — not by
`renderToolEntry` (≈10% cumulative). The tool cache was therefore **not** the
cause of that benchmark's crawl, and fixing it left those numbers unchanged.
The crawl is a stress artifact of regenerating a full live turn each frame; real
turns carry tens of tool calls where it is bounded, and the full-content path is
required when the user has paused scroll/selection. `BenchmarkToolEntryRender`
above is the correct, cache-focussed guard.
## Secondary observation — per-delta live-tail copies the whole history off the fast path

`renderPaneContent` (`internal/tui/transcript.go`) returns `t.busyPrefix + tail`
each call. When the busy+follow fast path (`renderTailWindow` over the bounded
`busyPrefixTail`) is not taken — wheel-paused scroll, selection weaver active,
or a stale viewport — this is an O(committed-history) byte copy per streamed
delta.

Measured (`BenchmarkBusyRender_PerDelta`):

| turns | ns/op  | B/op    | allocs/op |
|-------|--------|---------|-----------|
| 200   | 167µs  | 454 KB  | 585       |
| 800   | 249µs  | 1.38 MB | 583       |

Allocations-per-op stays flat (~585) because the growth is a single large
copy, but bytes grow ~3x while the turn only grows 4x — a residual linear term
that will resurface on long turns in scrolled/selection views (the primary
cause the original CPU-crawl fix targeted).

## Positive results (no leak in the batch path)

- **Live heap during active streaming was tiny and stable** — 5–9 MB
  (`-inuse_space`); dominated by `runtime.mallocgc` plus glamour regexp /
  `gorilla/css` renderer init.
- **Goroutines stayed flat at 8** across all agents for the whole lifecycle —
  no leaked provider/stream/viewport goroutines; checkpoints matched at t=30.
- **RSS stayed flat (~38–40 MB)** across repeated samples; no process-level
  growth over a full run.
- **Batch mode is I/O bound**: a 30 s CPU profile sampled only 0.53% in-Goroutine
  time, spent in SSE JSON parsing and netpoll — an agent waiting on the provider
  stream, not CPU-bound.
- **Cumulative allocations are dominated by symmetric, bounded costs**:
  `encoding/xml` + `crypto/x509` certificate parsing (per-TLS-conn, not a leak)
  and regexp/syntax compile (one-time glamour engine init).

## Minor observations

- `markdownRendererCache` (global `sync.Map`, `internal/tui/markdown.go`) is
  keyed by theme+width and never evicted — bounded by distinct widths but it
  persists across `/new` sessions in a running TUI process.
- All glamour rendering serializes behind one `sync.Mutex` per renderer
  (`renderMarkdownFor`'s `r.mu.Lock()`); fine for the single-threaded TUI
  `View()`, but a bottleneck if any path renders markdown concurrently.
- `liveMarkdownCache` is a bounded single-slot cache — re-renders the full
  growing turn text once per new delta (unavoidable, and it defeats the
  ~80 ms spinner-triggered re-renders when no new delta arrived).
