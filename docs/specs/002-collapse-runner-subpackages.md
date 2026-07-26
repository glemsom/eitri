# Spec 002: Collapse shallow runner sub-packages back into runner/

**Status**: ✅ Implemented (PRs #871, #872, #873, v0.1.5)

## Problem

The `internal/runner/` package has 4 sub-packages, 3 of which are shallow wrappers that each contain only one file with one type or interface:

| Sub-package | Files | Lines | What it holds |
|---|---|---|---|
| `runner/runconfig/` | 1 | 93 | `RunConfig` struct + `FromConfig()` builder |
| `runner/broadcast/` | 1 | ~80 | `BrowserBroadcaster` + `BrowserEvent` types |
| `runner/adapters/` | 1 | 203 | `HistoryManager` / `Confirmer` interfaces + implementations |
| `runner/loop/` | 4 | 600+ | The real agent turn loop — deep, earns its boundary |

Tracing a run's start-to-finish flow requires crossing 4 package boundaries, each demanding a separate context switch (`import` → find file → understand type → trace usage). The extraction was a worthy intermediate step (ADR-0011), but the seams are so thin that the boundary cost outweighs the benefit now. A type like `HistoryManager` lives in `runner/adapters/`, its usage is in `runner/run.go` and `runner/loop/`, and the concrete session type lives in `internal/history/session.go` — that's 4 hops for one seam.

## Solution

Merge `runconfig`, `broadcast`, and `adapters` back into `runner/` as descriptive files (e.g., `runconfig.go`, `broadcast.go`, `adapters.go`). Keep `runner/loop/` separate — it earns its package boundary with real depth (the agent turn loop).

## User Stories

1. As a developer, I want to read a run's lifecycle from `StartRun` through to the agent loop without crossing multiple package boundaries, so that I can understand the full flow in fewer context switches.
2. As an LLM agent, I want to import a single `runner` package for all run-lifecycle types, so that I don't waste context discovering which sub-package holds which type.
3. As a contributor, I want to add a new run-configuration field without editing a file in a separate sub-package, so that simple changes stay simple.

## Implementation Decisions

**Files to merge:**

| Source | Target file in `runner/` |
|---|---|
| `runner/runconfig/runconfig.go` | `runner/runconfig.go` |
| `runner/broadcast/broadcast.go` | `runner/broadcast.go` |
| `runner/adapters/adapters.go` | `runner/adapters.go` |

**What stays in `runner/loop/`:** The agent turn loop (`loop.go`, `loop_helpers.go`, `tool_call.go`, `stream.go`) — it's genuinely deep logic with its own internal helpers.

**Approach:** Pure file moves + import path updates. Each merge is a mechanical operation: copy the file to the parent directory, update the package declaration, update all import paths that reference the old sub-package, delete the old file/directory. Then prune the `go/doc` package-level doc comments since `runner/doc.go` already describes the combined surface.

## Testing Decisions

- All existing tests continue to pass unchanged (no logic changes).
- The only test-impacted files are those that import the old sub-package paths (which get updated).

## Out of Scope

- Merging `runner/loop/` into `runner/` — it earns its boundary.
- Any logic changes or refactoring of the merged types themselves.

## Further Notes

This is a mechanical consolidation. It undoes the sub-package extraction from #691 now that the seams have proven thin enough to collapse back. ADR-0011 may be updated to reflect that the extraction was a useful intermediate step but the permanent state is flat.
