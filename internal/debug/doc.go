// Package debug provides HTTP trace recording and crash dump writing.
//
// It owns two subsystems:
//  1. Recorder — bounded, thread-safe capture of LLM provider HTTP traces
//     for troubleshooting in the debug API
//  2. Crash dumps — timestamped directories under ~/.eitri/crash-dump/ with
//     diagnostic files (error chain, goroutine stacks, session state, traces)
//
// Key types:
//   - Recorder — trace recorder (NewRecorder, Record, List, InFlight, Count)
//   - HTTPTrace — one recorded LLM provider request/response
//   - DumpOptions — input struct for WriteCrashDump (Error, ErrorChain, Stack, ...)
//   - RuntimeSummary — lightweight runtime counters included in crash dumps
//
// Key functions:
//   - WriteCrashDump — assemble and write a crash dump directory
//   - SanitizeConfig — redact secrets from config for dump safety
//
// Dependencies:
//   - internal/config — config sanitization
//   - internal/session — UISession snapshot in crash dumps
//
// Extension points:
//   - Add new diagnostic files to WriteCrashDump
//   - Adjust Recorder capacity via DefaultCapacity constant
//   - Add new fields to RuntimeSummary for richer crash context
//   - Add new fields to DumpOptions or crashInfo to extend crash.json
package debug
