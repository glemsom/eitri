// Package debug provides HTTP trace recording, structured log ring buffer,
// and crash dump writing.
//
// It owns three subsystems:
//  1. Recorder — bounded, thread-safe capture of LLM provider HTTP traces
//     for troubleshooting in the debug API; preserves the last non-2xx trace
//     in a dedicated slot that is never evicted by the ring buffer. Also
//     accumulates per-provider-per-model interaction metrics (calls, retries,
//     error counts by class, latency histogram, token totals, cache hit/miss)
//     exposed via Metrics()
//  2. RingBufferHandler — slog.Handler wrapper that retains the last N
//     structured log entries for inclusion in crash dumps
//  3. Crash dumps — timestamped directories under ~/.eitri/crash-dump/ with
//     diagnostic files (error chain, goroutine stacks, session state, traces, logs)
//
// Key types:
//   - Recorder — trace recorder (NewRecorder, Record, List, InFlight, Count, LastFailingTrace, Metrics)
//   - HTTPTrace — one recorded LLM provider request/response (includes ResponseHeaders, Usage, ErrorClass)
//   - MetricsSnapshot / ModelMetrics — JSON shape of the interaction metrics aggregate
//   - ErrorClass — structured capture-time error classification (ClassifyError)
//   - RingBufferHandler — log ring buffer (NewRingBufferHandler, Entries, Count)
//   - LogEntry — one captured structured log entry
//   - DumpOptions — input struct for WriteCrashDump (Error, ErrorChain, Stack, ..., FailingHTTPTrace)
//   - RuntimeSummary — lightweight runtime counters included in crash dumps
//   - SystemDiagnostics — system-level context (go version, OS/arch, CPU, memory, disk, env keys)
//
// Key functions:
//   - WriteCrashDump — assemble and write a crash dump directory
//   - SanitizeConfig — redact secrets from config for dump safety
//   - CollectSystemDiagnostics — gather system diagnostics for crash dumps
//   - ClassifyError — classify an LLM call failure from status + message at capture time
//   - WithAttempt / AttemptFromContext — thread the retry attempt number to the recorder
//
// Dependencies:
//   - internal/config — config sanitization
//   - internal/session — UISession snapshot in crash dumps
//
// Extension points:
//   - Add new diagnostic files to WriteCrashDump
//   - Adjust Recorder capacity via DefaultCapacity constant
//   - Adjust log ring buffer capacity via DefaultLogCapacity constant
//   - Add new fields to RuntimeSummary for richer crash context
//   - Add new fields to DumpOptions or crashInfo to extend crash.json
package debug
