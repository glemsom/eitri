# Render diagnostics

Use this workflow when the TUI feels slow, janky, or allocates too much memory. The goal is to isolate whether the bottleneck is model latency, transcript rendering, markdown layout, or viewport painting.

## Performance symptoms

- Cursor or stream updates lag behind model output.
- Scrolling through long transcripts stutters.
- Switching models or sessions causes a visible freeze.
- Memory climbs during long sessions without dropping.

## Supported workflows

### pprof alone

Profile the running eitri process to find where time or allocation pressure is spent:

```sh
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/profile
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/heap
```

Start eitri with pprof enabled:

```sh
eitri --pprof 127.0.0.1:6060
```

### Benchmark comparison workflow

Eitri's render path touches several seams. When a change may affect responsiveness, measure before and after one focused change with benchmarks; measure, change one thing, and re-measure.

Benchmark seams to isolate:

- **Model view** — turning raw stream deltas into the live answer bubble.
- **Transcript render** — replaying prior turns in the session view.
- **live turn rendering** — composing reasoning, tool calls, and answer text under streaming updates.
- **markdown rendering** — parsing and styling model output.
- **viewport rendering** — scrolling, wrapping, and terminal cell allocation.

Run the focused benchmark set:

```sh
go test -run '^$' -bench=. -count=10 ./internal/tui/... > old.txt
# make one change
go test -run '^$' -bench=. -count=10 ./internal/tui/... > new.txt
benchstat old.txt new.txt
```

Use `benchstat` for statistical comparison rather than eyeballing raw nanosecond deltas. If pprof shows hot work inside a render seam, pair the benchmark run with a CPU profile so the profile and the numbers point at the same code path.

Existing render benchmarks remain the starting point. Add a new benchmark only when the existing ones cannot express the seam you changed.
