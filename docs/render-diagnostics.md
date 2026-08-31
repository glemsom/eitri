# Render diagnostics

Use the diagnostics path that matches the symptom, then keep the evidence with the fix.

## Performance symptoms

Use `pprof` when rendering is slow, allocates too much, stalls while streaming, or regresses a benchmark. `pprof` is disabled during normal startup. Enable it only for a diagnostic run:

```sh
eitri --pprof 127.0.0.1:6060
```

The server refuses non-localhost binds. Use `localhost:0` or `127.0.0.1:0` in tests when an ephemeral port is needed.

Collect evidence from another shell:

```sh
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/profile
curl --fail --max-time 30 -o heap.pprof http://127.0.0.1:6060/debug/pprof/heap
curl --fail --max-time 30 -o goroutine.txt 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=2'
eitri --pprof 127.0.0.1:6060 --pprof-mutex --pprof-block
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/mutex
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/block
```

Mutex and block profiling are off unless their flags are supplied, because they add runtime overhead and are diagnostic evidence, not normal behavior.

## Visual correctness symptoms

Use render diagnostics when the screen is wrong: dropped transcript text, bad follow behavior, wrong viewport position, ANSI/style defects, wide-glyph alignment problems, or a frame that differs from the intended layout.

Render diagnostics are opt-in and are written where the caller configures them:

- `RenderDiagnosticFrames` and `RenderDiagnosticSummaries` are in-memory evidence for render cost, output size, viewport, follow, phase, message count, live-turn state, and related frame facts.
- `FrameSnapshotDir` receives bounded plain-text rendered frame snapshots named `frame-000001.txt`, `frame-000002.txt`, and so on.
- `RawFrameCaptureDir` receives bounded raw frame captures named `raw-frame-000001.txt`, `raw-frame-000002.txt`, and so on.

Plain-text frame snapshots and raw frame captures may contain transcript content, including prompts, assistant output, and tool output. Treat them as session evidence, not anonymous telemetry. In-memory frame stats and summaries should carry render facts rather than transcript bodies.

## Proving diagnostics-motivated performance fixes

Existing render benchmarks remain the starting point before adding new measurements. Likely seams are:

- Model view
- Transcript render
- live turn rendering
- markdown rendering
- viewport rendering

Use `pprof` to find where time or allocation pressure is spent, not to prove the fix. No performance claim is accepted based on pprof alone.

Run repeated benchmarks before and after one focused change, then compare with `benchstat`: measure, change one thing, and re-measure.

```sh
go test -run '^$' -bench 'Render|View|Transcript|Markdown|Viewport' -benchmem -count=10 ./internal/tui > /tmp/eitri-before.txt
# change one thing
go test -run '^$' -bench 'Render|View|Transcript|Markdown|Viewport' -benchmem -count=10 ./internal/tui > /tmp/eitri-after.txt
benchstat /tmp/eitri-before.txt /tmp/eitri-after.txt
```

If the existing render benchmarks do not cover the bottleneck, add the smallest benchmark at one of those seams, capture the before result, make the change, then re-measure. Treat single-run impressions as noise.

## Proving visual correctness fixes

Before changing rendering behavior, add or update a regression test before changing behavior. Prefer a test at the public render seam that failed for the captured symptom: viewport/follow state for scrolling defects, rendered plain-text snapshots for layout defects, and raw frame capture for ANSI/style defects. Then make the smallest code change that turns the test green.
