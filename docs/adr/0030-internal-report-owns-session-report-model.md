# 0030 — internal/report owns the Session Report model

**Status**: Accepted
**Date**: 2026-08-09

## Context

The Session Report turn concept lived in five shapes: `timeline.TimelineEvent`
(the timeline's persisted event), `report.Turn` (the assembled report turn),
`templates.TurnView` (a 1:1 exhaustive copy of `report.Turn` that differed only
in pre-rendered `ContentHTML`/`ReasoningHTML`), `report_helpers` (which
re-derived `turnHasLLMMeta` and `contextPercent` as template helpers), and raw
JSON re-unmarshalled on every read. When the model drifted, the copies drifted:
turn fields were declared twice, and the derivations were only testable through
browser-bound template tests.

## Decision

`internal/report` is the single owner of the Session Report model and behavior.

- **`report.Turn` is the canonical turn representation**, consumed by the report
  routes and templates. `TimelineEvent` remains the timeline's own persisted
  artifact.
- **The attribution heuristics live in `internal/report`**: `enrichFromSnapshot`
  (timestamp / snapshot-array-order user-message joining, issues #1159/#1160)
  and `enrichFromTraces` (turn → trace precedence: ID → (runID, turn) group →
  ±30s timestamp fallback, issue #988, plus summary retry recomputation).
- **The derivations `Turn.HasLLMMeta` and `ContextPercent` are report-module
  behavior**, tested without a browser.
- **`TurnView` stays a thin template edge projection** with pre-rendered HTML
  (templates never format markdown), but it embeds `report.Turn` — its
  duplicated field declarations are deleted and every model field is promoted.
- **Consumers no longer re-unmarshal persisted artifacts**; the persister's
  read side returns typed values (issue #1204).

No user-visible behavior change.

## Consequences

- One place owns the turn model, so field additions no longer need to be copied
  into a view model.
- Template rendering depends on `report` only for formatting; model behavior
  (telemetry presence, context percentage) is unit-testable without a browser.
- The report assembly remains layered: timeline projection (emission order),
  snapshot enrichment (user attribution), then trace enrichment (per-call
  telemetry and summary totals).
