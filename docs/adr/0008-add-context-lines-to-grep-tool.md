# Add context lines parameter to the `grep` tool

## Status

Accepted

## Context

The `grep` tool (introduced in an earlier ADR) returns matching lines only: `file:line:content`. When the LLM wants to edit matched code with the `edit` tool, it needs surrounding context to construct unique `old_text` anchors. Currently it must call `read` for context — one extra turn per edit.

The `edit` tool's `old_text` is most reliable when it includes several surrounding lines. Having grep return context lines directly makes the grep→edit workflow single-turn.

## Decision

Add an optional `context` parameter to `grep`:

```
grep(pattern: "...", file_pattern: "*.go", context: 3)
```

- Type `int`, optional, default `0` (preserves existing behavior)
- Returns N lines of context before and after each match
- Context lines use the same `file:line:content` format, with a visual separator showing match vs context — context lines prefixed with space, match line prefixed with `>` (or other visual hint)
- Respects existing 128 KiB output cap (may truncate with large context values)
- Default `0` ensures no change for callers that don't need context

## Considered Options

- **No context param, cross-reference descriptions instead**: LLM burns a turn calling `read` for context. Fragile when line ranges shift between grep and edit.
- **Always include 2 context lines (no param)**: Changes existing grep behavior. Some callers (file discovery, simple existence checks) don't need context. Bloat.
- **Context param with default 0 (chosen)**: No behavior change for existing callers. Power users get context when they need it.

## Consequences

- `grepArgs` struct gains a new `context` field
- Result format changes when `context > 0`: match lines visually distinct from context lines
- Tool description updated to describe context behavior
- 128 KiB cap may truncate large-context searches — truncation marker already handles this
- backward compatible: `context: 0` callers see identical output
