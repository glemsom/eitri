# Eitri vs OpenCode — Benchmark report

**Result: Eitri leads on this benchmark.** Both agents completed all 10 tasks successfully using `gpt-6-luna` (Eitri GitHub Copilot provider; OpenCode `github-copilot/gpt-6-luna`). This is one run per task, not a statistical trial; treat the measurements as directional, not universal.

## Executive scorecard

| Measure | Eitri | OpenCode | Eitri vs OpenCode |
|---|---:|---:|---:|
| Tasks successful | **10/10** | **10/10** | tied |
| Mean wall time / task | **17.86 s** | 77.20 s | **4.32× faster** |
| Total elapsed across tasks | **178.6 s** | 771.95 s | **593.4 s saved** |
| Mean reported gross tokens / task | **80,628** | 153,440 | **47.5% fewer** |
| Aggregate reported gross tokens | **806,278** | 1,534,401 | **728,123 fewer** |
| Mean model turns / task | **5.1** | 7.4 | **31.1% fewer** |
| Mean tool calls / task | **4.2** | 15.2 | **72.4% fewer** |

Gross tokens here are the benchmark’s reported input + output + cache-read categories. Provider accounting can overlap categories and is not necessarily billed usage; compare as a directional signal only. Both agents used the same named model, but Eitri and OpenCode have different harnesses, prompts, context handling, and provider integrations.

## Per-task comparison

| Task | Eitri tokens | OpenCode tokens | Eitri / OpenCode | Eitri time | OpenCode time |
|---|---:|---:|---:|---:|---:|
| 01 · Batch documentation | 8,373 | 23,995 | 0.35× | 3.4 s | 20.7 s |
| 02 · Config defaults test | 21,874 | 27,733 | 0.79× | 7.1 s | 12.1 s |
| 03 · Turn-range parsing | 77,641 | 118,228 | 0.66× | 13.5 s | 38.9 s |
| 04 · Session transcript docs | 144,869 | 135,712 | 1.07× | 12.2 s | 35.6 s |
| 05 · Batch accounting test | 205,859 | 831,403 | 0.25× | 26.3 s | 507.2 s |
| 06 · Usage documentation | 77,693 | 88,528 | 0.88× | 11.2 s | 26.5 s |
| 07 · OpenAI usage tests | 40,800 | 42,837 | 0.95× | 7.6 s | 20.4 s |
| 08 · JSON envelope test | 107,689 | 84,333 | **1.28×** | 33.4 s | 28.0 s |
| 09 · Invalid range errors | 102,004 | 121,449 | 0.84× | 58.2 s | 62.7 s |
| 10 · Usage regression test | 19,476 | 60,183 | 0.32× | 5.9 s | 19.8 s |

Eitri had lower reported token totals on **8/10** tasks, OpenCode on 2/10. Task 08 was Eitri’s only clear per-task loss in token volume: it spent 28% more gross tokens and took 5.4 seconds longer. Task 04 is close in tokens (Eitri +6.7%) despite faster elapsed time. Task 05 is the standout efficiency difference: OpenCode took 8.5 minutes and used about 4× Eitri’s tokens; inspect its trace before attributing this to any particular cause.

## Reproduction and artifacts

Run from the repository root:

```sh
python3 benchmarks/benchmark.py --model gpt-6-luna --agents eitri,opencode \
  --opencode-model github-copilot/gpt-6-luna \
  --output benchmarks/results/eitri-opencode-gpt-6-luna
```

Machine-readable results are in `results.json` and `results.csv`; per-run records, transcripts, stdout/stderr, and exact tasks are under `runs/`. The benchmark harness unit suite passed **16 tests** before the run.
