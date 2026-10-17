# De-monolithing `internal/tui`

`internal/tui` is unwound one package at a time: each extraction lifts a concern
with its own state and timers out of the `Model`/`Transcript` monolith into a
sub-package whose interface callers and tests cross the same way.

## Extracted

| Sub-package | Owns |
| --- | --- |
| `livekey` | the shared mutable session key `/new` re-mints. |
| `telemetry` | the status strip's aggregate: turns, tokens, cache, recovery. |
| `pulse` | the live turn's busy flag and elapsed clock, spinner and forge frames, tool-start glint, idle ember, and the derived `Phase`. Shared with the model; `Transcript` forwards to it. |

## Queued

| Concern | Why it is still inside |
| --- | --- |
| geometry | the `width/height/rail/railWidth` clamps (rail takes `n+1` columns, transcript minimum one column) are re-derived per frame at ~12 call sites in `rail.go`, `composer.go`, `settings.go`, `help_overlay.go`, `selection.go`, `model.go`. Deepen: one `Geometry` value computed at each size/rail/settings change and threaded as data. |
| pointer / selection | the hit-test on `Transcript`, coordinate mapping in `Model` (`selection.go`), and highlight/copy in `selectionweaver.go` are one concern across three files. |
| modal routing | `Update`, `viewString`, `overlayOpen`, and face-draw each probe Settings/Help/mention/slash by nil-check; one modal-ownership seam would let a new modal register instead of re-plumbing six sites. |
| composer | editing state (draft, recall-ring cursor, height, caret) lives on `Model` across `composer.go` and `prompt_history.go` around a bare `textarea.Model`. |

## Ground rules

- Move one concern per change. Stand the sub-package up behind a forwarding
  interface and keep the render, benchmark, and golden tests green before
  flipping call sites.
- The interface is the test surface: a new sub-package gets its own unit tests;
  the seam tests keep asserting the integration through `Transcript`.
- A shared instance (like `pulse`) is handed down through the construction seam,
  never duplicated, so the clock and the render cannot drift apart.
