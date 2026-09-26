# De-monolithing `internal/tui`

`internal/tui` is package `tui`: ~10k LOC of production code plus ~22k LOC of tests in one Go package. Every module can read and mutate every other module's unexported state, so a module boundary there is a prose claim, not a compiler-enforced one. This doc records how we migrate the hot modules into single-owner sub-packages, one leaf at a time, without behavioural change (`go test ./...` green after every step).

## Status

Done:

- `internal/tui/livekey` — `LiveSessionKey`. Already atomic (own mutex-guarded state, exported ctor + `Get`/`Set`, stdlib-only); extraction was pure relocation.
- `internal/tui/telemetry` — `Telemetry`. Already atomic. Extraction exported the `Apply`, `LiveContextSize`, `HitPercent`, and `Stats` seams, replacing the direct unexported-field reads that `rail.go` and tests used to make. Its unit test moved into the sub-package.
- The turn-session seam. `TurnSession` no longer reaches into `Transcript` — it supplies cancellable execution only, and `TurnRuntime.Begin` projects through `Transcript.Project`. `TurnFlow` and `Phase` are already separate package-level owners.

## Why the hot modules are not extractable by file-move alone

- `Transcript` is poked directly from most of the package: `model.go`, `composer.go`, `selection.go`, `settings.go`, `rail.go`, `login.go`, `skill_activation.go`, and `help_overlay.go` all read or mutate its unexported fields and methods.
- The render primitives (`render.go`, `live_markdown_cache.go`) depend on package-global state read all over the package: the 35-field `Theme`, the glyph resolver `lookup()`, `motionEnabled()`, `busySpinnerFrames`, `RenderMarkdown`, `Phase`, `phaseVerb`/`forgeVerb`. Extracting them means exporting or moving that shared surface too.
- The test surface references unexported state directly, so a move needs the affected unit tests to move into the sub-package and the integration tests to switch to the exported seam.

Each remaining leaf is a seam-export redesign, not a file relocation. Do them one at a time.

## The pattern (proven on `livekey`, `telemetry`)

1. Make the target module a genuine single owner *inside* package `tui` first: identify every direct field/method poke from other files and replace the reads with exported seam methods (`Apply`, `Stats`, ...) — tests still green.
2. Move the file into `internal/tui/<leaf>/` as `package <leaf>`, moving the leaf's focused unit test into the sub-package too.
3. Flip package-`tui` call sites to the imported seam; the package-wide unexported globals the leaf no longer sees (`Theme`, `lookup()`, spinner state) either move with it or become explicit parameters or fields on the seam.
4. Run `go test -count=1 ./...` and `go vet ./...` until green; commit.

## Remaining leaves, in order

1. **`internal/tui/transcript`** — `Transcript` + `toolLog` + `collapseFocus`. Requires exporting the ~40 members the rest of the package pokes directly. Largest export surface, but mechanically the pattern above.
2. **`internal/tui/render`** — `render.go` + `markdown.go` + `cheap_live_render.go` + `live_markdown_cache.go` + the `Phase`/caption console. Requires moving `Theme` (and its 35 fields) and the glyph/motion/spinner accessors into the render package, or passing them as explicit params on an exported `Render` seam. The original ticket called this the "cheapest first win", but the shared `Theme`/`lookup()`/spinner surface makes it a larger change than the leaf-moves above.
3. **`internal/tui/composer`** — `composer.go` + `completion_menu.go` + `mention.go` + `prompt_history.go`. Deepest coupling into `Transcript`'s theme and height; do it after the transcript seam exists.

Chrome (`styles.go`, `glyphs.go`, `spinner.go`, `face.go`) and the render dbx stay at the top of `internal/tui` until step 2; `theme` remains a package-`tui` type until then.

## Guardrails

- Every step is pure migration: no behaviour change, no new feature, no test-semantics change (assertions should read identically on both sides of a move).
- Run `go test -count=1 ./...` and `go vet ./...` after each step.
- Keep ARCHITECTURE.md's `internal/tui` section, and this doc, in sync after each landed leaf.
