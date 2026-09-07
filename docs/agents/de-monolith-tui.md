# De-monolithing `internal/tui`

`internal/tui` is package `tui`: ~7.8k LOC of production code + ~17.7k LOC of tests in one
Go package. ARCHITECTURE.md describes it as "small single-owner modules around a `Model`",
but that claim lives only in prose. Nothing in the build enforces it, and every module can
read and mutate every other module's unexported state. This doc records how we migrate the
hot modules into compiler-enforced single-owner sub-packages, one leaf at a time, without
behavioural change (`go test ./...` green after every step).

## Status

Done:

- `internal/tui/livekey` — `LiveSessionKey`. Already atomic (own mutex-guarded state, exported
  ctor + `Get`/`Set`, stdlib-only); extraction was pure relocation.
- `internal/tui/telemetry` — `Telemetry`. Already atomic. Extraction exported the `Apply`,
  `LiveContextSize`, `HitPercent`, and `Stats` seams, replacing the direct unexported-field
  reads that `rail.go` and tests used to make. Its unit test moved into the sub-package.

Not yet done: `transcript`, `render` (+ live caches), `composer`, `turn session`.

## Why the hot modules are not extractable by file-move alone

The premise that these are separable modules with a thin Model-facing seam is currently
false in the compiler. Concretely:

- `TurnSession.Begin` mutates `Transcript` internals directly (`tx.live = s`,
  `tx.appendUserMsg(prompt)`, `tx.busy = true`, `tx.busyStartedAt = ...`, `tx.log.SetAnchor`,
  `len(tx.messages)`).
- `model.go`, `composer.go`, `selection.go`, `rail.go` reach into `Transcript` and each
  other's unexported methods/fields (`m.tx.theme`, `m.tx.busy`, `m.tx.appendMsg`,
  `.tx.weaver`, `.tx.plainLines`, ...).
- The render primitives (`render.go`, `live_markdown_cache.go`) depend on package-global
  state that is read all over the package: `Theme` (≈30 fields), the i18n helper `g()`,
  `motionEnabled()`, `busySpinnerFrames`, `RenderMarkdown`, `Phase`, `phaseVerb`/`forgeVerb`.
  Extracting them means exporting/moving that shared surface too.
- The test surface is large and references unexported state directly, so a move needs the
  affected unit tests to move into the sub-package and the integration tests to switch to the
  exported seam.

So each remaining leaf is a real seam-export redesign, not a file relocation. Do them
one at a time.

## The pattern (proven on `livekey`, `telemetry`)

1. Make the target module a genuine single owner *inside* package `tui` first: identify every
   direct field/method poke from other files and replace the reads with exported seam
   methods (`Apply`, `Stats`, ...) — tests still green.
2. Move the file into `internal/tui/<leaf>/` as `package <leaf>`, moving the leaf's focused
   unit test into the sub-package too.
3. Flip package-`tui` call sites to the imported seam; the package-wide unexported globals the
   leaf no longer sees (e.g. `Theme`, `g()`, spinner state) either move with it or become
   explicit parameters/fields on the seam.
4. Run `go test ./...` (full, uncached) and `go vet ./...` until green; commit.

## Suggested order

1. **`internal/tui/livekey`**, **`internal/tui/telemetry`** — done (above).
2. **`internal/tui/session`** — `TurnSession` + `TurnFlow` + `fold`/`phase`. Hardest seam:
   `Begin` must stop mutating `Transcript` and instead return a declarative `TurnPlan`
   (prompt, busy flag, stream cursor, tool-log anchor) that the Model applies through a
   `Transcript` method. This is the biggest behavioural-neutrality risk in the migration.
3. **`internal/tui/transcript`** — `Transcript` + `toolLog` + `collapseFocus`. Requires
   exporting the ~30 methods/fields currently poked by `model.go`, `composer.go`,
   `selection.go`, `rail.go`, `turn_session.go`.
4. **`internal/tui/render`** — `render.go` + `live_markdown_cache.go` + the `Phase`/caption
   console. Requires moving `Theme` (and its ~30 fields) and the i18n/motion/spinner accessors
   into the render package or passing them as explicit params on an exported `Render` seam.
   This is the named "cheapest first win" in the original ticket, but it is not cheap — the
   shared `Theme`/`g()`/spinner surface makes it a larger change than the leaf-moves above.
5. **`internal/tui/composer`** — `composer.go` + `completion_menu.go` + `mention.go` +
   `prompt_history.go`. Deepest coupling into `Transcript.theme`/height and the turn session.

Chrome (`styles.go`, `glyphs.go`, `spinner.go`, `face.go`) and the `render` dbx stay at the
top of `internal/tui` until step 4; `theme` remains a package-`tui` type until then.

## Guardrails

- Every step is pure migration: no behaviour change, no new feature, no test-semantics change
  (assertions should read identically on both sides of a move).
- Run `go test -count=1 ./...` and `go vet ./...` after each step.
- Keep ARCHITECTURE.md's `internal/tui` section, and this doc, in sync after each landed leaf.
