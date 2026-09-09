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

### Kitty face upload is damage-driven, not a timer loop

The rail's kitty face is a static image; it must be uploaded exactly when its placement or size changes, never while the model is idle. The Model keeps a `faceDirty` flag: only window resize, rail-width change, theme change, and live renderer scroll set it, and the upload itself clears it, so the 50 ms `faceDrawTick` loop dies as soon as the face is clean. An idle Eitri on kitty/ghostty uploads the face once at boot (via the startup `WindowSizeMsg`) and then issues no kitty escapes until the next real damage — no background re-upload and no steady 20 fps repaint on battery.

The behavioral guard is in `internal/tui/face_test.go`:

```sh
go test ./internal/tui -run 'TestFaceUploadsOnceAtBootThenIdles|TestResizeReuploadsFace|TestRailWidthChangeReuploadsFace|TestThemeChangeReuploadsFace'
```

`TestFaceUploadsOnceAtBootThenIdles` proves an idle model answers a stray face-draw tick with no command at all (no upload, no re-arm); the other three prove each damage class (terminal resize, rail-width tweak, theme save) re-uploads on the next face draw.

### Committed-history render-cost guard

A long conversation must not cost more per turn just because history is long. The committed render memo (`Transcript.units`) makes committing a new turn render only that turn and serve the prior units from the memo, so per-turn commit cost stays flat in prior-history length instead of re-rendering (and re-wrapping) the whole transcript each commit — the quadratic crawl this memo exists to remove.

The regression guard for that property is the size-sweep in `internal/tui/committed_render_cost_test.go`:

```sh
# deterministic threshold, runs in the normal test suite
go test ./internal/tui -run TestCommittedCommitCostFlatInHistorySize

# empirical wall-clock / allocation surface
go test ./internal/tui -run xxx -bench BenchmarkCommittedCommitCost -benchmem -benchtime 30x
```

The size-sweep builds N committed turns for N in {10, 100, 1000} and measures the marginal cost of committing one more. "Flat" means the marginal commit re-renders exactly the new turn's two committed units (its prompt + its answer) and nothing else, at every N — never the prior history. If a change re-derives prior units on commit, the marginal cost exceeds 2 and the excess grows with N, so the 1000-turn case flags the regression where a small fixture would not. The benchmark's alloc count should stay near zero and flat across N; growth with N is the same regression surfacing empirically.

### Input-scoped memo invalidation

Invalidation of the committed render memo is scoped to the inputs that actually feed it, so an in-place committed change never re-renders the whole history. An expansion toggle, a committed tool observation, or a block-focus marker move marks only the unit(s) whose flow draws the affected block (`invalidateCommittedUnit`); a width, theme, or expand/collapse-all change re-wraps everything and drops the whole memo (`invalidateCommittedMemo`). After every invalidation the memo must serve bytes a fresh full render would produce.

The behavioral guard is in `internal/tui/committed_scoped_invalidation_test.go`:

```sh
go test ./internal/tui -run 'TestReasoningToggleRerendersOnlyItsUnit|TestToolToggleRerendersOnlyItsUnit'
```

The deterministic thresholds: an expansion toggle re-renders exactly the toggled block's unit (1 fresh committed render) and nothing else — the rest of the memo is served as-is; a width or theme change re-renders all committed units but the result stays byte-identical to a fresh full render (`assertLayoutMatchesFreshFullRender`). The size-sweep form (`TestScopedToggleCostFlatInHistorySize`, N in {10, 100, 1000}) proves a toggle's invalidation cost stays at exactly one unit regardless of prior-history length, the same flat-cost property under a different input. A regression that falls back to whole-memo drops makes these counts grow with N.
