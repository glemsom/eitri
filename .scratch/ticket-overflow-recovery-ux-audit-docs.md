# Ticket: Update overflow recovery UX marker, audit visibility, and docs

## Type
UX / docs

## Problem
The current user-facing marker and docs use compaction language, which frames summarization as ordinary maintenance. The new behavior is rare emergency context overflow recovery and should be surfaced that way.

## Goal
Make overflow recovery visible and understandable to users when it happens, and update docs to describe the new behavior.

## Scope
- Change the live user-facing marker from `[compacted]` to:

```text
[context overflow: summarized older history and retried]
```

- Ensure session review/audit makes recovery occurrence visible at least as a simple marker/event.
- Update user-facing docs/help strings that describe this feature.
- Do not perform broad internal renames in this ticket.
- Do not change unrelated uses of `compact` that mean concise display, unless easy and clearly beneficial.

## Likely files
- `internal/engine/events.go`
- `internal/tui/telemetry.go`
- `internal/tui/rail.go`
- `internal/tui/telemetry_test.go`
- `internal/tui/rail_test.go`
- `internal/app/app.go`
- `README.md`
- `docs/sessions.md`

## Acceptance criteria
- Live marker is exactly:

```text
[context overflow: summarized older history and retried]
```

- Session review/audit clearly shows that overflow recovery happened.
- README/docs no longer describe proactive compaction as a tunable session feature.
- Docs explain that context overflow recovery is enabled by default and can be toggled in TUI/settings.
- `go test ./...` passes.

## Resolution
Resolved in this branch:
- Updated the TUI rail audit marker from `state compacted` to `recovery context overflow`.
- Kept the live stderr marker as `[context overflow: summarized older history and retried]` from the runtime work.
- Updated session docs to describe context overflow recovery visibility in `messages.jsonl` instead of post-compaction histories.
- Updated comments/tests away from proactive compaction wording where touched.
- Verified `go test ./...`.
