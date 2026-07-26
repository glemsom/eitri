# Spec 001: Unify llm.Message and session.Message into a single canonical type

## Problem

The codebase has two nearly identical `Message` structs: `llm.Message` (in `internal/llm/types.go`) and `session.Message` (in `internal/session/session.go`). The `session.Message` is a superset of `llm.Message` with UI extras (`ReasoningContent`, `CreatedAt`, `Components`, `QuickReplies`). Every data path that crosses from persistence → LLM API or LLM API → UI requires manual conversion:

- `internal/persist/persister.go` — `sessionMessagesToHistory()` and `historyToSessionMessages()`
- `internal/runner/adapters/adapters.go` — both `sessionHistoryManager` and `requestHistoryManager` previously converted between the two types; now they use the canonical type directly

This forces anyone (human or LLM) reading a code path to constantly ask "which type am I looking at? Where does the conversion happen? Which fields survive?" Two types that are nearly 1:1 make every data-flow path twice as long to trace.

## Solution

Collapse into a single canonical `Message` type carrying all fields (LLM wire fields + UI extras). The old types are removed and all call sites use the canonical type directly.

## User Stories

1. As a developer, I want to trace a message from the LLM API response through persistence to the UI without bouncing between two nearly-identical types, so that I can understand data flow in half the context.
2. As an LLM agent reading the codebase, I want to see one Message type used everywhere, so that I don't need to track conversion logic between parallel types.
3. As a contributor, I want to add a new field to chat messages without updating two structs and their conversion functions, so that features land with less boilerplate.

## Implementation Decisions

**Where the canonical type lives:** `internal/llm/message.go` — the `llm` package is the lowest layer that already defines the wire format, and both `internal/session/` and `internal/persist/` already import it. Placing it here avoids circular imports.

**What the canonical type carries:**
- All fields from `llm.Message` (Role, Content, ToolCallID, ToolCalls)
- All fields from `session.Message` (ReasoningContent, CreatedAt, Components, QuickReplies)
- JSON struct tags on all fields for direct serialization

**Approach:** Expand-contract. Phase 1 adds the unified type alongside existing ones (no breakage). Phase 2 migrates call sites in dependency order (llm → session → persist → runner → api). Phase 3 removes old types.

## Testing Decisions

- Existing tests continue to pass after each migration step (no behaviour change).
- Conversion functions (`sessionMessagesToHistory`, etc.) are removed entirely — no need to test what no longer exists.
- The serialization format of session snapshots on disk must remain stable (existing JSON snapshots remain loadable).
- New tests verify that the canonical type round-trips through JSON with all fields intact.

## Out of Scope

- Schema changes to the LLM wire format (OpenAI/Anthropic JSON) — the canonical type is an internal representation.
- Adding new fields beyond what currently exists in either `llm.Message` or `session.Message`.

## Further Notes

This is a pure refactoring — no feature change, no new behaviour. The entire benefit is in reduced cognitive load for future readers and writers.
