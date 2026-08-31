# Render diagnostics

## Localhost pprof diagnostics

`pprof` is disabled during normal startup. Enable it only for a diagnostic run:

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

## Proving diagnostics-motivated performance fixes

Existing render benchmarks remain the starting point before adding new measurements. Likely seams are:

- Model view
- Transcript render
- live turn rendering
- markdown rendering
- viewport rendering

Use `pprof` to find where time or allocation pressure is spent, not to prove the fix. No performance claim is accepted based on pprof alone.

Run repeated benchmarks before and after one focused change, then compare with `benchstat`:

```sh
go test -run '^$' -bench 'Render|View|Transcript|Markdown|Viewport' -benchmem -count=10 ./internal/tui > /tmp/eitri-before.txt
# change one thing
go test -run '^$' -bench 'Render|View|Transcript|Markdown|Viewport' -benchmem -count=10 ./internal/tui > /tmp/eitri-after.txt
benchstat /tmp/eitri-before.txt /tmp/eitri-after.txt
```

If the existing render benchmarks do not cover the bottleneck, add the smallest benchmark at one of those seams, capture the before result, make the change, then re-measure. Treat single-run impressions as noise.
