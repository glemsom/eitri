# Changelog

All notable changes to Eitri are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The canonical UI session store (`internal/session`) gains the two history
  behaviours only the old LLM-history store had (issue #1239): the
  **exchange-cap sliding window** and **pending-tool-use repair**. The shared
  logic lives in `internal/message/exchange.go` as pure functions over the flat
  canonical `Message` shape — `TrimExchanges` (drops the oldest exchanges when
  the user-message count exceeds the cap, keeping the trailing assistant/tool
  tail — exactly the history store's sliding-window semantics) and
  `RepairPendingToolUse` (closes a trailing unresolved assistant tool call with
  a synthetic tool error result so a resume never appends a user message after
  an unclosed tool use) — with `DefaultMaxExchanges` (150) as the single
  canonical default both stores resolve to (`history.DefaultMaxExchanges` now
  aliases it). The `session.Manager` trims on every append path
  (`AppendMessage`, `AppendToConversation`) and gains a `RepairPendingToolUse`
  method; `ReplaceConversationMessages` does **not** trim, matching the history
  store's `RestoreHistory` (compaction / live-sync write-back is never
  trimmed). The cap is configured via the new `session.WithMaxExchanges`
  constructor option (non-positive → default, like
  `history.NewSessionManager`); production wires `cfg.MaxHistory` to both
  stores in `cmd/eitri/main.go`, so they work side by side with the same cap.
  **Parity tests** (`internal/session/exchange_parity_test.go`) drive both
  stores through identical inputs and assert identical conversation shapes for
  trim and repair, so both stores behave identically before the history store
  is contracted away (umbrella #1231).

- The `write` and `edit` tools can now target configured **writable roots** —
  the same `sandbox.extra_writable_paths` paths the `bash` tool may write to —
  outside the workspace root (#1210). Both tools accept targets relative to the
  workspace, absolute within it, or absolute within any writable root, and a
  `/tmp/...` target is rewritten to the run's session-scoped sandbox tmpdir on
  the host (the same file `bash` sees at that path, ADR-0026), passing through
  to host `/tmp` when no sandbox shadow is tracked. A target outside the
  workspace and all writable roots is a hard error (no confirmation prompt).
  Both tools' `path` JSON-schema descriptions document the writable-root and
  `/tmp` behaviour. The writable roots and sandbox-manager `TmpdirFor` callback
  are threaded at the single base-tool-registry construction site, so UI,
  batch, and sub-agent runs behave identically.

- New ADR-0031 records the decision to let the `write` and `edit` tools target
  configured **allowed write paths** — the same `sandbox.extra_writable_paths`
  roots the `bash` tool can already write to — outside the workspace root, with
  `/tmp` targets rewritten to the run's sandbox shadow dir (extending ADR-0026).
  Docs-only: the tool code changes ship in a follow-up. (#1208)

- `internal/fileutil` gains a shared writable-path resolver for the `write`/`edit`
  tools (#1209): `ResolveWritablePath` rewrites a `/tmp/...` target to the run's
  session-scoped sandbox tmpdir on the host when tracked (the same
  `open_in_browser`/`sandbox.Manager.TmpdirFor` mapping per ADR-0026, with
  identical pass-through fallback when untracked), then validates the result
  against the workspace root and configured writable roots via
  `ValidatePathWithAllowed`, hard-erroring on targets outside all roots (no
  confirmation prompt). Pure prefactor — no tool behavior changes until the
  follow-up wiring ticket lands; resolver behavior is fully unit-tested.

- `docs/ARCHITECTURE.md` no longer describes `write`/`edit` as strictly
  workspace-only (#1211): the `internal/tool/` file-map entries for `write.go`
  and `edit.go`, the tool-registration prose, the run sequence diagram, and the
  resource-access invariant now state that byte-tools validate targets against
  the workspace root **and** the configured writable roots (allowed write
  paths, ADR-0031), with `/tmp/...` targets rewritten to the run's session
  sandbox tmpdir (ADR-0026) — matching the ADR and glossary terminology.
  Docs-only.

- `scripts/agent-loop.sh` wires the build→test→review pipeline into the per-issue
  worker phase (T6, #1192): each issue now runs a bounded fix loop driven by pure
  bash — the `code-build` persona implements/opens the PR (via `build_pr`), the
  `code-test` gate (`test_pr`) rejects/accepts it, and only a test-passing PR goes
  to the `code-review` gate (`review_pr`). Test `REJECT` and review
  `CHANGES_REQUIRED` bounce back to a fresh `code-build` round handed the current
  `.test.md`/`.review.md` and PR; review `BLOCKED` consumes the shared cap
  immediately. The 3-round cap (shared across both reject paths) leaves the PR open
  with a "needs human" comment and moves on. The dispatcher's merge queue now only
  merges when bash tracks the latest test verdict = `PASS` AND the latest review
  verdict = `APPROVED` (`can_merge_issue`), reusing the existing serialized
  rebase-first `merge_pr`. `docs/agents/batch.md` documents the loop.

- `scripts/agent-loop.sh` gains the `review_pr` gate (T5, #1190): it runs the
  `code-review` persona as a fresh batch via the shared verdict plumbing and
  maps its outcome to `APPROVED`/`CHANGES_REQUIRED`/`BLOCKED`/`hard-fail`. The
  `code-review` persona (`docs/personas/code-review.yaml`) loads the
  `code-review` skill (Standards + Spec axes) and writes currently-open review
  findings to `.review.md` before emitting its verdict line. The gate only ever
  sees test-passing PRs (the build→test→review order guarantees it) and decides
  nothing about loop policy (cap / re-entry / merge land in T6, #1192).
  `docs/agents/batch.md` documents the gate.

- `scripts/agent-loop.sh` gains the `test_pr` gate (T4, #1191): it runs the
  `code-test` persona as a fresh batch via the shared verdict plumbing and maps
  its outcome to `PASS`/`REJECT`/`hard-fail`. The `code-test` persona (`docs/personas/code-test.yaml`) now emits an explicit `VERDICT: PASS`/`VERDICT: REJECT` line and writes currently-open findings to `.test.md`, noting the no-test-suite downgrade when applied; `extract_verdict` recognises `PASS`/`REJECT` beside the review verbs. The gate decides nothing about loop policy (cap / re-entry / merge land in T6, #1192). `docs/agents/batch.md` documents the gate and the expanded verdict verbs.

### Added

- Ships three adaptable **example** personas for the review-gated issue loop under `docs/personas/`: `code-build` (the implementer), `code-test` (the verifier gate), and `code-review` (the reviewer gate), each a named system-prompt bundle with accurate `required_skills` (per ADR-0018). They are templates — copy to `~/.eitri/personas/<name>.yaml`, not auto-installed. `docs/agents/batch.md` now documents the user-level-only persona resolution (the runner ignores a workspace `.eitri/personas/` directory) and links the examples. (#1189)

- The agent-loop dispatcher now prints a rich per-issue worker summary when each worker finishes (#1215): the worker's overall exit status and wall-clock duration, and one line per stage that ran (build / test / review / resolve) with its latest verdict (PASS / REJECT / APPROVED / CHANGES_REQUIRED / BLOCKED / OK / hard-fail, or `(none)` when the persona emits no VERDICT line) and, when that batch carries a session ID, the route to its persisted session trail under `~/.eitri/sessions/<id>/`. Each stage still streams to its own per-stage log file, so parallel workers never interleave on the terminal; claim/rebuild/test/review/merge behaviour is unchanged.

- `scripts/agent-loop.sh` gains two reusable verdict-plumbing helpers for the review-gated pipeline (T3, #1188): `run_persona_batch <wt> <stage> <persona> <prompt>` runs one persona as a fresh batch in a worktree (streaming to `log.$stage`, with the worker-phase `setsid --wait` shield and 130/143 Ctrl+C loop) and prints the verdict / exit status / log path, and `extract_verdict <log>` parses the latest `VERDICT: ...` line out of a log. A non-zero exit or a missing verdict both surface as a distinguished `hard-fail` (never a blind retry); the helper only runs a batch and reports a verdict — the shared cap / re-entry / merge policy will land in T6 (#1192). The script is now sourceable (its dispatcher entry point is guard-claused) so T4/T5 and tests can reuse the helpers without starting the loop. `docs/agents/batch.md` documents both.

- New `open_in_browser` tool opens a single `http`/`https`/`file` URL or file path in the user's host browser via `xdg-open`, run in-process by the unsandboxed harness so it can reach the host X11/Wayland socket (ADR-0026, #1173). Schemes other than `http`, `https`, and `file` are hard-rejected without launching anything; a bare path is normalized to `file://` (resolved against the workspace when relative, verified to exist); and a sandbox `/tmp/...` path is rewritten to the matching host file written by `bash` for the run. It is silent (no confirmation prompt), visible in the transcript, available to sub-agents via the base toolset, and shares its launcher with the `EITRI_OPEN_BROWSER` startup auto-open, which now waits up to ~10s for `xdg-open` and reports its exit/stderr instead of fire-and-forgetting.

- The `bash` tool's sandbox `/tmp` is now **session-scoped** (ADR-0026, #1172): one host directory is mounted at `/tmp` for every sandboxed `bash` call of a run, so a file written to `/tmp` in one command survives for the rest of the run instead of vanishing on the next call. Each run's `/tmp` is isolated from other runs and is removed automatically when the run ends, including on error paths. Sandbox-disabled configs are unchanged (no tmpdir created, direct host execution).

- Tool argument schemas now carry hard, JSON-Schema-expressible input bounds so the model validates ranges up front instead of relying on prose defaults: `read.start_line`/`read.end_line` `minimum: 1`, `web_fetch.timeout` `minimum: 1`/`maximum: 120`, browser `navigate.timeout` `minimum: 1`, and `delegate.max_turns` `minimum: 1`. Bounds mirror existing runtime defaults (read 1–100, web_fetch 15s, navigate 30s, delegate 250); runtime behaviour is unchanged. (#1168)

- The UI now gives every interactive element a full hover, pressed (`:active`) and visible keyboard-focus (`:focus-visible`) treatment, drawn from a canonical `--focus-ring` token (based on the accent colour) so focus feedback reads in both light and dark themes. Interactive controls — sidebar session/activity rows, nav links, quick-reply chips, completion/persona/directory-browser items, buttons, `<summary>` rows and the run selector — now expose all three states. (#1116)

- The narrow-viewport story is now designed and verified for the three surfaces that previously only inherited the single 768px sidebar collapse: the sessions management table scrolls horizontally (`min-width` + scroll container) instead of squashing its columns, the tool-activity list gets a bounded full-width scroll region, and the report page stacks its header chrome and wraps its run-selector/meta bars for tighter gutters. (#1116)

- CI now splits tests into a fast **unit+race** job (`make test-race`, all non-browser packages under the race detector) and a standalone **browser E2E** job (`make test-browser`, chromedp, no `-race`). The chromedp browser suite in `internal/api` is now gated behind the `//go:build e2e` tag, so `go test ./...` and the release gate no longer drag slow browser/DOM tests through `-race` — a browser flake no longer blocks the fast unit gate or every commit, and each result is attributable to its own CI job. Docs updated in `docs/TESTING.md` and `CONTEXT.md`; the verbose `make release-check` still runs both suites. (#1122)

- Tests gains a new `testutil.GoroutineLeakGuard` helper that verifies a service started and stopped repeatedly (compact runs, run start/cancel loops) does not leave a background goroutine running into the next test case, closing the shutdown audit from issue #1127. Registration is one line atop any test; it polls the goroutine count back to baseline at teardown and dumps stacks on a leak. Documented in `docs/TESTING.md`.

- The persona add/edit forms now pre-fill the system-prompt field with the canonical default prompt as a freely editable **starting point** (issue #1140). The new-persona form always pre-fills; the edit form pre-fills only when the existing prompt is empty (a non-empty prompt is left verbatim). The default is sourced from the single canonical `persona.DefaultPrompt` constant, so a user specialising a persona does not silently lose the concise/be-focused/reasoning-budget guardrails.

- Personas can now declare an opt-in **visible skills** list. A persona that sets `visible_skills` surfaces only those named skills' entries in the agent's `<available_skills>` catalog (narrowing what the agent sees as available) instead of always listing every effective skill; leaving it blank keeps today's behaviour. It is separate from `required_skills`, which remains a first-turn load directive. Distinct and unified with the persona editor UI as a multi-select, mirroring required skills. (#1139)

- Batch and sub-agent runs now share one per-turn run-completer (`runCompleter`): the batch turn-completer and the sub-agent snapshotter are merged into a single implementation (ADR-0025). Both wire the same completer through the `loop.HistoryManager` conversation-source seam (session-manager-backed for batch/UI, request-based for sub-agents), so per-turn running-status snapshots, the shared auto-compaction step, and the terminal snapshot + timeline on every exit path (completed / cancelled / max-turns / error) are each written by one implementation. Behaviour is unchanged for every run kind; this is a pure internal consolidation. (#1107)
- CI now runs a diagnostic **`reproduce-flaky`** job on every push/PR that reproduces environment-driven test flakes on the constrained configuration (`go clean -testcache`, `-cpu 1,2`, `-p 1`, with the race detector) before they reach the main `make test-race` gate. The job is **non-blocking** (`continue-on-error`) — failures surface as a warning on the push/PR without failing the workflow — and uploads the raw log as the `test-race-flaky-output.log` artifact on every run. `docs/TESTING.md` documents the path to promote it to a mandatory gate once it provably catches a real flake. (#1126)
- The UI design-token set is now fully canonicalized: each semantic role maps to exactly one token name in the `:root` root(s), and the half-migrated alias names that duplicated a token value (`--muted`, `--border-muted`, `--danger`, `--text-secondary`, `--bg-secondary`, `--bg-tertiary`, `--fg`, `--fg-muted`, `--mono-font`) were removed with every component usage consolidated onto the canonical token. No visual change — pure token-name consolidation for a single source of truth. (#1112)
- Delegated (sub-agent) runs now prepare through the same unified run-preparation seam (`prepareRun`) as UI and batch parents, but as a leaf: `SpawnSubAgent` calls `prepareRun` with `allow_delegate=false`, so a sub-agent's tool registry is the base toolset + `skill` — exactly as before, with no `delegate`/`collect`/`render_quick_replies` (no recursion, no UI-only tools) — and its system prompt is still the parent persona + task suffix. Recursion/leaf gating now lives as a config option on the seam (ADR-0025) rather than in a separate sub-agent registry assembly; behaviour is unchanged. (#1106)
- Sub-agent runs (`delegate`/`collect`) now auto-compact their conversation history exactly like parent runs: the sub-agent turn completer persists its per-turn snapshot and then runs the same shared auto-compaction step UI and batch parent runs use, so a long sub-agent task (e.g. a 250-turn delegate cap) whose request-based history exceeds the configured high-water mark compacts to below the low-water mark instead of overflowing the context window and failing mid-task. Compaction settings are inherited from the parent run config (`compaction_enabled`, threshold/low-water percentages, message-size threshold, tool-call retention turns, salience, context window), the compacted history is written back to the run's request via a new replace-history capability on the history seam (`loop.HistoryManager.ReplaceHistory`, implemented by both the session-manager and request-based adapters), and the follow-up snapshot under `~/.eitri/sessions/<taskID>/session.json` reflects the compacted history (child session trail unchanged in shape). Parent-run compaction behavior is unchanged. (#1096)
- Batch runs (`eitri -b`) now stream reasoning/thinking deltas from reasoning models to stdout as they arrive, delimited by `[thinking]`…`[/thinking]` markers so the reasoning content is distinguishable from the final text — batch output now carries the same thinking content the UI shows live. Models without reasoning are unchanged: their output remains plain text with no markers. (#1095)
- Sub-agents now have the `skill` tool: the shared base tool registry (`buildBaseToolRegistry`) registers the skill tool when a skills service is wired, so sub-agents spawned via `delegate` can load skills in both UI and batch modes. A sub-agent inheriting a persona with required skills gets the `<required_skills>` directive in its system prompt (the persona/repo-instructions/skills-catalog prompt contract is now shared with parents) and can load each required skill via `skill()` on its first turn instead of burning a turn on an unknown tool. `skill` is a leaf capability — `delegate`, `collect`, and `render_quick_replies` remain parent-only, so sub-agents still cannot recurse or emit UI-only tools. ADR-0013 amended (toolset = base + `skill`; persona-inheriting prompt contract). (#1092)
- Batch runs (`eitri -b`) now auto-compact their conversation history exactly like UI runs: the batch turn completer persists its per-turn snapshot and then runs the same shared auto-compaction step the UI uses, so a long batch run whose history exceeds the configured high-water mark compacts to below the low-water mark instead of overflowing the context window and failing where the same prompt would succeed in the UI. All compaction settings in `~/.eitri/config.json` (`compaction_enabled`, threshold/low-water percentages, message-size threshold, tool-call retention turns, salience) are honored identically in both modes, the compacted history is reflected in the on-disk `session.json` snapshot (a follow-up snapshot is written after compaction), and UI compaction behavior is unchanged. Sub-agent turn completion remains snapshot-only (no compaction). (#1093)
- UI and batch parent runs now share one run-preparation seam (`prepareRun`): for the same config and prompt they produce an identical tool registry, system prompt contract, and LLM request. Batch mode gains the Agent Skills support it was missing — the skills catalog and `<required_skills>` directive enter the system prompt and the `skill` tool is registered, so personas with required skills work exactly as in the UI — and now applies `max_output_tokens` from config, sets a session-scoped prompt-cache key, releases browser-tool connections when the run ends (`EndSession`), and writes a crash dump on a panic inside the batch agent loop. `render_quick_replies` remains UI-only. Sub-agent runs share the same request builder (`max_output_tokens`, prompt-cache key, thinking level). Documented as ADR-0024. (#1091)
- The frontend now respects the `prefers-reduced-motion` and `prefers-reduced-transparency` user preferences. Under reduced motion, every decorative/infinite animation collapses to a static render: the avatar glow pulses become a static box-shadow (stream status stays visible without looping), and the face/avatar breathing, typing dots, refresh spinner, connection-test opacity pulse, and the undo-toast countdown width animation are disabled (`animation: none`; the toast still auto-dismisses on its JS timeout). Under reduced transparency, every `backdrop-filter` glass surface — the gear dropdown, persona dropdown, completion menu, confirmation/directory-browser modals, and the sticky form-actions footer — drops the backdrop blur and substitutes a solid token background; the modal overlay scrim keeps its dimming background but loses its blur. Both modes are pure CSS (no functional breakage, no JS changes). (#1075)
- Sub-agent runs (`delegate`/`collect`) are now persisted as child sessions under `~/.eitri/sessions/<taskID>/` in both UI and batch modes: a `session.json` snapshot (parent linkage, title derived from the task, full conversation with tool calls, system prompt, workspace) is written before the run starts and after every complete agent turn, a terminal snapshot (idle/error) and a per-run timeline with the correct termination reason (completed / cancelled / max-turns / error) are written on every exit path, and the sub-agent's HTTP traces now land under the child session's `traces/` (previously dropped because no `session.json` existed for their directory). The in-memory sidebar child session behaviour is unchanged. (#1041)
- Batch runs (`eitri -b`) now persist the same reviewable trail as UI sessions under `~/.eitri/sessions/<id>/`: a `session.json` snapshot (id, prompt-derived title, status, full message history with tool calls, system prompt, workspace) rewritten after each complete agent turn and on exit (`idle` on success, `error` on failure), per-call HTTP traces under `traces/` (the async trace queue is drained on both success and failure paths instead of dropping queued traces on failure), and a per-run timeline under `timeline/` with the correct termination reason (completed / cancelled / max-turns / error). The session ID defaulted to `batch-<unixnano>` and could be named via `EITRI_BATCH_SESSION_ID` (validated: non-empty, no path separators, no `..`); that env override was removed when IDs became auto-generated. Existing consumers are unchanged — session reports and on-demand session load work on batch sessions exactly as on UI sessions. (#1039)
- Agent turns are now correlated to HTTP traces by ID at write time: each trace records its `run_id` and 1-based `turn`, an `llm_call` SSE/timeline event carries the successful attempt's `trace_id` plus retry count and timing, and the session report joins turns to traces by ID (the ±30s timestamp heuristic is demoted to a fallback for legacy data). Per-turn retries are surfaced in the report (attempt count + failed attempts before success, plus a `total_retries` run summary), and time-to-first-token (request start → first content token) is recorded per trace and shown in the report. (#988)
- The debug API now exposes the full persisted HTTP trace archive via `GET /api/debug/traces` (query by `session_id` / `provider_id` / `model` / RFC3339 time range, with `limit`/`offset` pagination) and `GET /api/debug/traces/aggregate` (window aggregate over the same filters: count, error rate, p50/p95 latency, and token totals). Traces survive restarts on disk and are now queryable historically, not just through the in-memory ring buffer; traces of permanently deleted sessions are excluded. (#989)
- `GET /api/debug/metrics` exposes aggregate per-provider/per-model LLM health counters accumulated by the HTTP trace recorder: total calls, retry count, error counts by structured class (`rate_limit`, `timeout`, `auth`, `context_length`, `network`, `other`), a latency histogram, token totals (prompt, completion, cache read/write), and cache hit/miss counts. Error classes are classified once at capture time from the HTTP status code and the error message (never by scanning error text at display time). HTTP traces gain the supporting fields (`model`, `attempt`, `finish_reason`, `usage`, `error_class`); for streaming responses longer than the 256KB body cap the usage/finish_reason are recovered from the stream tail instead of being truncated away. Batch (headless) runs and sub-agents feed the same recorder and counters as browser runs. (#987)
- HTTP traces now record one accurate per-LLM-call measurement record: provider-reported token usage (prompt, completion, cache-read/cache-creation, reasoning), `finish_reason`, model name, retry attempt number, and time-to-first-byte. The run-done SSE event now carries the provider-reported usage when available (the text-length estimate is used only when the provider returned none), and the session report shows the enriched fields per turn plus aggregate cache token counts. (#986)
- The `CalibrationStore` now persists per-model chars-per-token observations to disk (`~/.eitri/calibration.json`, or `$EITRI_DIR` when set) and restores them on startup, so calibration data survives restarts instead of resetting to the default 4.0 chars/token. An absent or empty file falls back to current defaults; the store is saved on shutdown (server mode) and at the end of batch runs. (#985)
- The Session Manager now exposes shared read-only accessors (`GetMetaShared`, `GetConversationShared`, `GetConfigShared`) that return the session's internal state directly as references instead of deep-copying the entire conversation on every call. They are documented as manager-owned ("do not mutate") and are the preferred read API going forward. Purely additive — no behaviour change. (#979)
- The LLM retry policy is now configurable per run: `RunConfig.RetryPolicy` (retry attempts + backoff delay) threads through to the agent run loop, and tests inject zero-attempt or zero-backoff policies so dead-endpoint runs fail in milliseconds instead of sleeping 5×1s. Production defaults are unchanged (5 retries, 1s backoff). (#1024)
- Inter and JetBrains Mono are now self-hosted: the UI font stack is bundled as embedded `woff2` assets under `/static/fonts/*` instead of being loaded from `fonts.googleapis.com`. The Google Fonts `<link>`/preconnect tags are gone, the critical latin subsets are `<link rel=preload>`ed in the page head, every `@font-face` uses `font-display: swap` (text renders immediately on slow/offline loads), and the service worker precaches all 13 font files so the app shell is fully offline-capable with zero third-party CDN requests. (#970)
- Static assets under `/static/*` are now served with `Cache-Control: public, max-age=31536000, immutable`, so the ~4.7MB JS/CSS bundle is fetched once and served from cache on every later navigation/reload instead of being re-downloaded. Every asset reference (page shell, JS islands, service-worker precache, PWA manifest icons) carries a `?v=<content-hash>` cache-bust query string derived from the embedded asset contents, so a release changes the URLs and stale JS/CSS can never survive an upgrade. The service worker script is served with `Cache-Control: no-cache` and embeds the same version, so a release updates the worker and drops its old precache automatically. HTML pages and SSE streams are unaffected and remain uncached. (#969)
- The page shell now loads all core scripts with `defer` (non-render-blocking), and the heavy rendering libraries — mermaid (~2.7MB), KaTeX, and Prism — are no longer fetched on every page load. `eitri-lazy-load.js` fetches them on demand only when a diagram, equation, or code block is actually present, and re-scans after every HTMX swap. The service-worker precache list matches the new strategy (heavy libraries are cached on first use). (#968)
- The `browser` tool now exposes a per-action typed JSON schema to the model instead of a free-form args blob: `browser.action` is an `enum` of valid actions and `args` is a discriminated union (`oneOf`) with one typed branch per action carrying its exact required parameters. The hand-rolled `query`→`selector` fallbacks for common LLM mistakes are removed — the schema itself tells the model the selector field is called `selector`. On-wire action names and behaviour are unchanged. (#955)
- `browser` tool gained four new actions: `new_tab` opens a fresh tab and returns its `target_id`, `close_tab` closes a tab, `select` sets an HTML `<select>` dropdown to a given option value, and `get_value` reads back the current value of a form element. All go through the deadline-bounded `prepareTarget` path so a hung CDP connection can't block the agent loop. (#954)
- Tool schema generator (`SchemaOf`) now supports `jsonschema_enum`, `jsonschema_minimum`/`jsonschema_maximum`, `jsonschema_min_items`/`jsonschema_max_items`, and `jsonschema_item_description` struct tags, so tool schemas can carry `enum`, numeric bounds, and array-length constraints for the LLM. `browser.action` now exposes its valid actions as an `enum`. (#950)

- The AFK agent loop is specced to gate every issue through a **build → test → review** pipeline before merge (ADR-0027, #1187): deterministic bash in `scripts/agent-loop.sh` drives three example personas (`code-build`, `code-test`, `code-review`) as fresh-context batch runs; a mandated final `VERDICT: APPROVED | CHANGES_REQUIRED | BLOCKED` line is extracted from each review log (missing verdict or non-zero exit = hard-fail); `.review.md`/`.test.md` hold currently-open findings only and are handed to each fix re-entry; review `BLOCKED` or either reject path consumes shared rounds up to a 3-round cap (exceeded → leave PR open, comment "needs human", move on); and merge happens only when bash tracks the latest test verdict = PASS and latest review verdict = APPROVED. Pipeline order guarantees a test-failing PR is never sent to review. This ADR is the contract all review-gated-loop tickets conform to; the bwrap-sandboxed in-tree `go test`/`make test` verification is a stated acceptance gate.

### Changed

- The loop's session-backed history adapter (`loop.NewSessionHistoryManager`)
  now reads and writes the **canonical session store** (`internal/session`)
  instead of the separate LLM-history store (`internal/history`), for both UI
  and batch runs (issue #1241). The run-completer's per-turn
  history→conversation copy (`ReplaceConversationMessages` + the sync module)
  disappears from the UI snapshot path — the UI conversation, the LLM request
  history, and the persisted snapshots all read the same canonical
  conversation, so tool-attached components/quick replies survive persistence.
  `RunService` no longer carries a `HistorySessionMgr` dependency: UI runs
  append the user message to the canonical store synchronously in `StartRun`
  (the chat handler no longer double-appends), batch runs create their session
  in the canonical store, manual compaction reads/writes the canonical
  conversation through the same adapter seam, and loading a session from disk
  (`LoadSessionFromDisk`) restores its conversation directly into the
  canonical store (the old `RestoreHistory`-into-history-store hydration is
  gone — the canonical store IS the run's history). LLM request history is
  byte-identical (a parity test drives the new adapter against the old history
  store through identical operation sequences), and snapshot/report tests pass
  unchanged. One deliberate fidelity caveat: the read-side round-trip folds
  `ReasoningContent` written into the canonical store by other paths
  (`syncRunResultToUISession`, persister-less configs only) into the content
  `TextBlock` ("R\nC") and drops `RawContent` — a match to the old store,
  whose LLM history never carried either field; adapter-written messages carry
  no reasoning, so the fold never affects ordinary UI/batch runs. The old
  `internal/history` store remains only for the parity tests until it is
  contracted away (umbrella #1231, issue #1242).

- Trace aggregation now has a single owner (issue #1240): the window-aggregate fold (count, error count/rate, p50/p95 latency, token totals, window bounds) that the persisted archive aggregate endpoint returns is implemented once — `debug.AggregateTraces` in the new `internal/debug/aggregate.go` — and `Persister.AggregateTraces` delegates to it instead of re-implementing the fold on disk (its previous `aggregateTraces` explicitly "mirrored the recorder's metrics"). All debug trace endpoints now parse their filter parameters (session/provider/model/time/limit/offset) through one shared parser, so the in-memory recorder lists (`/api/debug/http` and `/api/debug/sessions/{id}/http`) accept the same parameter surface as the persisted endpoints (`model`/`from`/`to`/`offset` are accepted and ignored by the in-memory lists, which filter only on session/provider/limit) and reject malformed parameters with `400` exactly like the archive endpoints. The derived token total (the sum of the four usage components, not each trace's stored total) is likewise single-owned — `UsageTotals.TokenTotal` — used by both the recorder's metrics snapshot and the window aggregate. `/api/debug/metrics` (the recorder's richer per-provider-per-model counters) and `/api/debug/traces/aggregate` return the same data as before; Session Report enrichment is unchanged. The in-memory endpoints' docs now match the code: `limit` defaults to no limit (everything the recorder holds, capped at 100) and `provider_id` filters within the path session on `/api/debug/sessions/{id}/http`.

- The "session was permanently deleted" check is consolidated into one helper (issue #1237): `Persister.sessionExistsOnDisk` is now the single owner of the "no `session.json` on disk" check, used by the snapshot loader (`readSessionSnapshot`/`LoadSession`/`LoadSessionInfo`), `SaveTrace`, `Flush`, and the trace query surface (`scanTraces` — `QueryTraces`/`AggregateTraces`) instead of four inline `os.Stat`/`os.ReadFile`-based copies. The check is `IsNotExist`-aware: only a definitive "no `session.json` on disk" counts as permanently deleted, while other stat failures (EACCES, ENOTDIR, …) surface at each site exactly as they did before the consolidation (the snapshot loader and trace query surface return the wrapped error; `SaveTrace`/`Flush` fall through to the write path). Trace save/flush/restore/query behaviour is unchanged: a session whose `session.json` is gone is treated as permanently deleted everywhere, so its traces are never recreated, persisted, or resurrected by queries.

- The per-turn history→conversation sync is extracted into one tested module (issue #1235): `internal/message/sync.go` now owns converting a run's LLM history (`[]EitriMessage`, system prompt prepended on reads) into the flat conversation shape (`[]Message`, system prompt stored separately) via `message.SyncHistoryToConversation`, with `message.StripLeadingSystemMessage` as the single home of the strip-system-message invariant (ADR-0028). The unified `runCompleter` (UI live-sync and `buildUISession`) and manual compaction (`CompactSession`) funnel through the module, deleting the turn-completer's hand-rolled `historyToMessages`/`stripLeadingSystemMessage` pair. Behavior is unchanged; the seam where the history store and the UI/snapshot conversation meet is now documented in `docs/ARCHITECTURE.md` and covered by unit tests for system-message stripping, tool-call/tool-result mapping, empty history, and compacted-message passthrough.

- The run ID is now generated **exactly once per run** at run start and plumbed through the run options and the terminal seam (issue #1234): UI, batch, and sub-agent runs compute `GenerateRunID` a single time and pass the value into `loop.RunOpts.RunID` (stamped onto every HTTP trace) and the unified `runCompleter` (stamped onto the persisted timeline), instead of recomputing it 2–3 times per run. The persisted timeline, SSE-derived events, and HTTP traces of one run now share a single identifier by construction, so the Session Report's turn↔trace correlation can never drift apart (issue #988). No user-visible behavior change — the derived IDs were already coincident.

- The run service's dead surface is stripped (issue #1232): the deprecated `AppendEvent` no-op, the `RunCfg` field on `RunState` (set but never read), and the `lookupRun` alias of the active-run map lookup are deleted; the run tracker and batch-persistence file entries in the runner package doc are corrected (the tracker was folded into the service, batch persistence lives in the unified run-completer). Session-status broadcasting now flows through exactly one helper (`broadcastStatusUpdate`): the UI run-exit paths (`setSessionStatusAndSnapshot`) and the sub-agent completion path both funnel through it, replacing the two near-identical run-service helpers and the parameterized variant. No user-visible behavior change.

- The dead max-turns SSE special-case in the UI run-exit path is deleted (issue #1233): the agent loop already emits the max-turns error and closes the SSE stream itself (`loop.RunAgent` broadcasts `SSEWriter.Error` before returning `MaxTurnsExceededError`), so the UI exit path no longer appends the max-turns message to the SSE buffer or emits a `done` event after stream close — both were no-ops (the event was silently dropped and the appended text was neither broadcast nor persisted). The max-turns branch now keeps only the terminal status snapshot and the timeline write; the run still ends idle with the error message visible over SSE. Batch and sub-agent max-turns handling is unchanged.

- The terminal-layer unification promised by ADR-0028/0029 is complete (issue #1238): all three run transports — UI, batch, and sub-agent — now finish every exit path (completed / cancelled / max-turns / error) through the single run-end seam `runExit` in the new `internal/runner/run_exit.go`. The seam classifies the exit (`classifyRunExit`, the shared ADR-0029 taxonomy), dispatches the transport's per-reason work through the **single per-reason exit switch** (`exitWork.run` — the cancelled / max-turns / error branching previously hand-rolled in both `run.go` and `subagent.go` now lives in exactly one place, so adding a termination reason touches one file), writes the terminal snapshot + timeline (`runCompleter.terminal`), and then runs optional `afterTerminal` work (the UI's session-status broadcast, so subscribers never observe the terminal status before the snapshot is on disk). The UI transport no longer re-implements status snapshot + timeline writes on every exit path; it keeps only transport-specific work in `uiExitWork` (run-end append of the streamed reply, SSE closing events, crash dump, status broadcast), and its terminal snapshot now uses a plain-`CopySession` source so it preserves UI-only messages (tool components / quick replies) instead of re-running the per-turn live-sync. Batch runs pass a nil `exitWork` (no per-reason work). Because the run-end block is directly callable, exit paths are now covered by direct unit tests (`run_exit_test.go`: seam dispatch + nil-work + UI exit work + sub-agent exit work — the sub-agent max-turns path previously had no direct coverage) instead of only full-service async runs. Two cosmetic consistency changes on UI cancelled-run timelines: the `Run cancelled` SSE closing event is now recorded in the persisted timeline (error-path timelines already recorded theirs).

- CI's `browser-e2e` job is now a deterministic **regression gate** for browser E2E timing flakes (issue #1219): it runs the chromedp suite shuffled and repeated (`scripts/test.sh --e2e --shuffle --repeat 2` → `go test -shuffle=on -count 2 ./internal/api/`), so a single-test wall-clock race (lazy-lib deadline expiry, stale CDP node, unrendered response) surfaces on the PR instead of passing once and randomly breaking `main` after merge. The diagnostic `reproduce-flaky` job is promoted from non-blocking to a mandatory blocking gate (`continue-on-error: false`), so constrained-environment flake reproductions now fail the push/PR; the fast `unit-test` job is unchanged. Reproduce the gate locally with `make test-browser-gate`; `scripts/test.sh` gains `--shuffle` / `--repeat N` flags covered by `scripts/flaky_test.sh`.

- The report module now gets a single service identity wired through `ServerConfig` (issue #1206): `report.Service` is constructed once at startup in `cmd/eitri/main.go` and injected as `ServerConfig.ReportService`, and the four report handlers — list reports, get report, report page (HTML), and report fragment (HTMX run-swap) — consume the injected service instead of calling `report.New(persister)` per request. Handler behavior is unchanged; servers without a report service (no persister) keep returning the same empty run list and 404 responses (the nil-service 404 body now reads `report service not configured`).
- `internal/report` is now the single owner of the Session Report model and behavior (ADR-0030): `report.Turn` is the canonical turn representation consumed by the report routes and templates, `TimelineEvent` stays the timeline's own persisted artifact, and the attribution heuristics (`enrichFromSnapshot` timestamp / array-order user joining, `enrichFromTraces` ID → (run, turn) group → ±30s fallback) live solely in `internal/report`. The derivations `turnHasLLMMeta` and `contextPercent` moved out of the templates into `report` as `Turn.HasLLMMeta` and `ContextPercent`, testable without a browser. `templates.TurnView` is now a thin edge projection that embeds `report.Turn` and adds only pre-rendered `ContentHTML`/`ReasoningHTML` — its duplicated field declarations are deleted, and `makeTurnViews` no longer copies every model field. No user-visible behavior change. (#1205)

- The persister's read side now returns typed artifacts instead of raw bytes (issue #1204): `LoadTimeline` returns `*timeline.Timeline`, `LoadSession` returns `*session.UISession` (nil when no snapshot exists; a corrupt snapshot returns an error wrapping the new `ErrCorruptSnapshot` sentinel), `LoadTrace` returns `*debug.HTTPTrace`, and `ListTraces` returns `[]*debug.HTTPTrace` (unreadable/corrupt trace files skipped — the same skip-and-continue the report consumer already applied). Raw-file consumers keep the raw surface via the new `ListTraceFilenames`/`ClearAllTraces` pair: clear-all-traces deletes every trace file by on-disk filename, so corrupt or unreadable trace files are still removed (pre-refactor behavior). Consumers — `internal/report` (timeline/snapshot/trace reads; the reconstructed report still degrades to an empty report when a snapshot is corrupt), `internal/runner` (`LoadSessionFromDisk` re-marshals the typed snapshot once for the session manager's byte-based seam), and `internal/api` (cleanup UI) — no longer `json.Unmarshal` persisted artifacts. No user-visible behavior change.

- UI per-turn completion now runs through the unified `runCompleter` path shared by batch and sub-agent runs (ADR-0028): `RunService` no longer implements `loop.TurnCompleter`; a UI-mode `runCompleter` is wired per run with a snapshot-source seam that live-syncs the run's live conversation into the UI session and snapshots via `CopySession` (preserving full UI-session fidelity — `ActiveSkills`, `ClosedAt`, `RenderedMessageIDs`), while batch/sub-agent runs keep building the facade from history via `buildUISession`. The "strip leading system message" invariant collapses into one place (`stripLeadingSystemMessage`), deleting the hand-rolled copies in the old UI turn completer and `CompactSession`. No user-visible behavior change; UI compaction toasts/events are unchanged. (#1201)
- Run termination now flows through a single exit taxonomy (`classifyRunExit`, ADR-0029) shared by the UI, batch, and sub-agent transports: one function classifies a finished run into its terminal snapshot status and timeline termination reason (completed / cancelled / max-turns / error), replacing the three inline classifications in `run.go`, `batch.go`, and `subagent.go`. **Batch terminal status alignment**: batch cancelled and max-turns runs now persist a `StatusIdle` terminal snapshot (was `StatusError`), matching UI and sub-agent semantics — only true failures persist `StatusError`. The batch CLI exit code is unchanged: it is driven by `BatchRun`'s returned error, not the snapshot status. (#1202)

- The header chrome is polished so it reads as spacious and intentional (issue #1180): the header padding grows from `0.5rem 1rem` to `0.75rem 1.5rem`, and the Eitri brand title is bumped to `1.2rem` SemiBold (keeping its existing negative letter-spacing). The workspace path is restyled as an interactive chip (surface tint, border, radius, padding) that truncates with an ellipsis on a long path, and the stream status renders as a tinted pill whose background and edge are derived from the semantic status token via `color-mix`, so it stays theme-correct in both dark and light mode. On narrow viewports the header keeps a single row and the workspace chip caps at `40vw`. The gear dropdown and persona selector are unchanged.

- Chat message bubbles are redesigned to break the generic uniform-card wall (issue #1179): assistant messages are flat, cardless, full-width rows sitting on the page background with only a thin (2px) left accent border, while user messages become right-aligned accent-tinted bubbles (~12% opacity) with the avatar anchored to their right edge. The message list gap grows from `0.5rem` to `1rem` for breathing room, assistant content paragraphs use `text-wrap: balance` so lines wrap without orphaned words, and neither bubble carries a heavy card shadow. Streaming animations (glow/breathe), quick-reply chips, and sidebar tool cards are unchanged.

- The dark theme's palette is now a neutral charcoal instead of blue-navy, the accent is desaturated to a warm rose, and every `box-shadow` token is tinted to the charcoal base instead of pure black (issue #1178). `--bg` moves from `#1a1a2e` to `#0d0d12`, with `--surface` / `--surface-alt` / `--surface-raised` stepped up as a neutral surface ladder and `--glass-bg`, `--header-bg`, `--surface-overlay`, `--llm-meta-bg` and `--turn-context-bg` re-based on the charcoal surfaces. The accent is desaturated from `#e94560` (red-pink) to `#c26f6b` (warm, ~45% saturation). The dark-mode PWA `theme-color` meta follows the new charcoal base. Semantic status colours remain pastel and readable on the new palette; the light theme tokens are unchanged. The sessions, tools and thinking sidebar panels each get a distinct subtle background tint so the sections read as separate surfaces.

- The sidebar now reads as four distinct visual zones instead of one undifferentiated dark column (issue #1181): every panel (sessions, tools, thinking, context) carries its own subtle background tint, the section labels switch from uppercase to sentence case in the SemiBold body font, session items gain a left-accent border on hover, the thinking panel's font is bumped to `0.82rem` on a faint code-block background, and the context-panel progress bar is thickened to 10px with fully rounded ends (on both the track and the fill). Tool-activity typography stays on the `0.82rem` hierarchy at compact widths too. Dark and light themes both carry the same structure.

- The Settings system-prompt field is now the **generic persona's prompt** (issue #1141): editing it mirrors the value into `~/.eitri/personas/generic.yaml` and the UI labels/describes it as such, instead of it being a silent top-precedence override that shadows every active persona. A healthy active persona's own prompt still wins at run time; the settings prompt applies when no persona is active or when the active persona is missing/corrupt.
- Batch session IDs are now auto-generated by the shared run-ID helper (`runJobID`, ADR-0025): `eitri -b` no longer reads the `EITRI_BATCH_SESSION_ID` environment override, so every batch run leaves its review trail under an auto-generated `~/.eitri/sessions/<id>/` directory (snapshot, HTTP traces, timeline) and reports the ID in its output. `scripts/agent-loop.sh` no longer exports `EITRI_BATCH_SESSION_ID`; each worker's log carries the run's `session_id`, and the dispatcher prints each issue's session-dir path after the worker finishes so the trail remains reviewable without pre-naming it. Break-in for any caller that relied on setting `EITRI_BATCH_SESSION_ID` to choose the session directory. (#1109)
- `scripts/agent-loop.sh` now processes `ready-for-agent` issues in parallel instead of strictly sequentially. The script is a dispatcher: it claims up to `-j N` issues (default 2), adds an `in-progress` label to each, runs one `eitri -b` worker per issue in a detached git worktree (`.worktrees/issue-N`, worker output to `.worktrees/issue-N/log`), then serially rebases and squash-merges the resulting PRs. Rebase conflicts spawn a focused `eitri -b` resolution run capped at 3 attempts; leftover PRs stay open with a comment and are reported. Stale `in-progress` labels are cleaned up on the next startup. Ctrl+C/SIGTERM stops claiming after the current batch finishes (workers run via `setsid --wait`); a second signal forces an immediate exit. (#1035)
- `scripts/agent-loop.sh` workers previously named their batch sessions per issue via `EITRI_BATCH_SESSION_ID=issue-N`, leaving a reviewable `~/.eitri/sessions/issue-N/` (snapshot, HTTP traces, timeline) instead of an opaque `batch-*` directory; that env override was removed when session IDs became auto-generated, and per-issue review trails are now located by each worker's reported `session_id`. (#1040)

- Live context and compaction estimates now use the calibrated chars-per-token ratio for the current model: the context panel's `context_update` events (`ComputeContext`) and the auto-compaction high-water gate (`compactSessionHistory`) pass the active `CalibrationStore` and model name instead of a nil store and empty model, so both call sites stop falling back to the default 4.0 chars/token once calibration data exists. Default behaviour is unchanged when no calibration data is present. (#985)
- HTTP trace persistence is now non-blocking: completed traces are handed to a bounded async worker (`SaveTraceAsync`) so disk I/O never blocks the LLM request path, and the recorder's `OnComplete` callback now fires after the recorder mutex is released so parallel sessions never serialize on a trace write. The shutdown flush drains the async queue before persisting remaining recorder traces, so every trace still reaches disk with no loss or duplication. In-flight trace tracking is now capped (64 by default); when the cap is reached the oldest in-flight trace is evicted with an `evicted` marker (fail-safe) instead of growing without bound — a response body that is never read or closed can no longer leak the in-flight map. (#984)
- The Session Manager read path no longer deep-copies the conversation. `Get`/`GetValidated` and the shared accessors now return cheap O(1) shared references (no per-read message allocation; the facade is a single fixed-size allocation regardless of conversation length), and the copying getters (`GetMeta`, `GetConversation`, `GetConfig`) are gone. The few callers that genuinely need a detached snapshot — JSON snapshot serialization, the ChatPage/ReportPage template rendering path, and the debug endpoints (polled concurrently with active runs) — now call explicitly-named copy helpers (`CopySession`, `CopyMeta`, `CopyConversation`, `CopyConfig`). Benchmark: `Get` on a 1000-message conversation is ~79 ns/op / 1 alloc vs ~35 µs/op / 180 KB for `CopySession`. (#981)
- Session callers have been migrated to the shared read accessors from #979: every read-only call site of the copying getters (`Get`/`GetValidated` ownership checks, `GetMeta`, `GetConversation`, `GetConfig`) now reads via `GetMetaShared`/`GetConversationShared`/`GetConfigShared`, eliminating the per-request full-conversation deep copy on the chat, render, compact, skills, workspace, and run-status paths. The copying getters stay for callers that genuinely need a detached facade — JSON snapshot serialization, the ChatPage/ReportPage template rendering path, and the debug endpoints (which are polled concurrently with active runs) — each documented at its call site. (#980)

- Per-request HTTP logging is now suppressed in test binaries, so failed-test output dumps stay lean — production servers still log every request at Info level. Test coverage for the request-log fields moved to a direct unit test. (#1031)
- `make test` and `make test-race` now print a single compact verdict line (packages passed/failed, failing test names, and DATA RACE warnings with `-race`) plus only the failing tests' error excerpts, instead of one boilerplate line per package. Full raw output is teed to `dist/test-output.log` / `dist/test-race-output.log` for on-demand grepping, and the exit code mirrors `go test`. (#1032, #1033)

### Fixed

- The session-backed history adapter's `History()` filters UI-only **empty
  assistant placeholders** out of LLM request history (issue #1241 AC2):
  `session.Manager.AppendComponent`/`SetQuickReplies` append a bare
  `{role:"assistant"}` placeholder into the canonical store whenever the last
  conversation message is not an assistant message — which happens on the
  second and subsequent component-emitting tool calls of a turn (component
  emission runs before `AppendTool`, so the previous tool result is last). The
  old LLM-history store never carried these placeholders, so unfiltered they
  reached the next turn's LLM request as content-less `{"role":"assistant"}`
  messages — the exact shape providers reject (the old store's
  `AppendAssistant` and the loop's turn-end guard both skip empty assistant
  messages). `History()` now drops assistant messages with no content and no
  tool calls on read (the canonical store keeps them as UI component targets;
  because compaction reads and rewrites the conversation through this
  LLM-view `History()`, a compaction also drops the empty placeholder
  scaffolding — components attached to real assistant messages survive); the
  parity test's operation sequence is unaffected. Regression coverage: a
  unit test drives a component-emitting multi-tool turn through the adapter
  and asserts no empty assistant reaches `History()`, and a loop-level test
  runs the turn through `RunAgent` and asserts the next LLM request contains
  no empty assistant message.

- The session-backed history adapter's `History()` no longer reads the live
  canonical conversation without a lock (issue #1241 fix round): `History()`,
  the UI snapshot source (`uiSnapshotSource`), and batch run-start's
  `extractLastMessages` all read the conversation through
  `session.Manager.CopyConversation` — a snapshot taken under the manager lock
  — instead of iterating the shared reference returned by
  `GetConversationShared` after its lock has been released. Manual compaction
  (`POST /api/sessions/{id}/compact`) calls `History()` with no active-run
  guard, so the unsynchronized read could overlap the run goroutine's appends
  to the same message list (a data race: torn reads could drop/duplicate
  messages in the compactor's output → permanent conversation corruption).
  Regression coverage: race-detector tests drive `History()`,
  `uiSnapshotSource`, and `extractLastMessages` concurrently with live
  appends.

- Fixed a browser-E2E flake class across the final-render tests
  (`TestBrowser_ThinkingRendering`, `TestBrowser_HTMXBeforeEndTargetsMessages`,
  `TestBrowser_ToolCardsInScrollContainer`, and the streaming-markdown finals),
  where a completed run could paint an empty bubble (or none) in the live view.
  The agent loop now commits the final assistant message to the conversation and
  runs the per-turn live-sync **before** broadcasting the terminal `done` SSE
  event, so the frontend's finalize `/render` request (fired from `done`) always
  reads a conversation that already contains the finished reply — instead of
  racing the commit and swapping in an empty response.

- Trace identity is now single-owner across restarts (#1236): fresh trace IDs
  always advance past the restored archive — `Recorder.LoadAll` reseeds the
  ID generator from the largest restored `trace_N` (never moving it backwards)
  — the persister marks restored traces as already persisted so the shutdown
  `Flush` never re-writes them, and `SaveTrace` refuses to overwrite an
  existing trace file (`ErrTraceExists`) instead of silently clobbering an
  archived trace with a colliding fresh ID.

- The browser E2E persona-selector keyboard test
  (`TestBrowser_PersonaDropdownKeyboard`) no longer flakes on the #1219
  regression gate. The header selector is fetched into the empty
  `#persona-selector-container` via an htmx `hx-trigger="load"` request, and
  htmx attaches each option's `hx-post` activation listener in a **deferred
  settle task after the swap** — so a selector that is merely *visible* can
  still be missing its click listeners. The test used to start keying as soon
  as the selector appeared, so the Enter activation's synthetic click could
  land on an option with no htmx listener attached yet and silently do nothing
  (dropdown stayed open, label unchanged; ~30% failure rate under the gate's
  `-shuffle=on -count 2`). It now waits for the selector's options to be
  htmx-wired — each option's `htmx-internal-data.listenerInfos` carries a
  `click` trigger — before interacting, the same content-driven readiness
  pattern as the #1217/#1218 de-flakes. (#1219)

- The browser E2E composer test (`TestBrowser_ComposerEnterSendsAndShiftEnterAddsNewline`)
  no longer flakes with `No node with given id found (-32000)`: it used to chain
  chromedp selector actions (`WaitVisible` then `Text`) across the post-Enter HTMX
  re-render of the user bubble, and the swap invalidated the CDP node id resolved by
  the first action. It now polls the DOM for the rendered `.message-user` content,
  re-resolving the element on every iteration. `TestBrowser_ScrollSentinelPosition`
  no longer trips "assistant response did not render": its server now runs with a
  persister (so the run-completer live-syncs the assistant turn into the UI session
  and the final bubble actually renders content instead of depending on the browser
  catching the live stream before the instant fake run finishes), and it waits for
  the response via a generous content-driven poll after the composer is fully
  wired. (#1218)

- The browser E2E test server helper `newTestServerWithRuns` now builds its
  server **with a run persister wired in**, fixing the same structural
  no-persister defect for the rest of the browser suite: the run-completer only
  live-syncs committed turns into the UI session when a persister is configured,
  so tests that assert rendered assistant content after a fast fake run
  (streaming-markdown formatting, thinking rendering, fast-run/run-status,
  HTMX-before-end, scroll sentinel) previously never saw the committed assistant
  bubble and timed out their content polls. All of them now pass deterministically
  instead of only when the browser happened to catch the live stream before the
  instant fake run finished. The scroll-sentinel and mid-run-rejoin tests now use
  the shared helper instead of duplicating the persister wiring inline. (#1218)

- The browser E2E composer test (`TestBrowser_ComposerEnterSendsAndShiftEnterAddsNewline`)
  no longer flakes with `No node with given id found (-32000)`: it used to chain
  chromedp selector actions (`WaitVisible` then `Text`) across the post-Enter HTMX
  re-render of the user bubble, and the swap invalidated the CDP node id resolved by
  the first action. It now polls the DOM for the rendered `.message-user` content,
  re-resolving the element on every iteration. `TestBrowser_ScrollSentinelPosition`
  no longer trips "assistant response did not render": its server now runs with a
  persister (so the run-completer live-syncs the assistant turn into the UI session
  and the final bubble actually renders content instead of depending on the browser
  catching the live stream before the instant fake run finishes), and it waits for
  the response via a generous content-driven poll after the composer is fully
  wired. (#1218)

- The streaming-markdown **final-render browser E2E tests**
  (`TestBrowser_StreamingMarkdownFinalRender*`) no longer fail on slow/CI
  runners. The shared `streamingMarkdownTestHelper` now first waits for the
  heavy on-demand rendering library the final-render content needs (Prism /
  KaTeX / Mermaid, loaded lazily since #968) to actually arrive in the browser
  before it starts polling the render assertion, and the tight 8-second poll
  deadline is replaced by a generous 30-second fallback guard — the lazy-lib
  wait plus the content-driven checks are the real completion signal, so a slow
  on-demand lib fetch or stream flush can no longer expire the deadline and
  fail a correct render. The Mermaid assertion now requires the diagram's
  rendered SVG (not merely the raw `pre.mermaid` source block), so a mermaid
  load/render regression genuinely fails the test. (#1217)

- UI runs on a **persister-less RunService** (the exact configuration browser
  E2E test servers use) now put the run's streamed reply into the in-memory UI
  conversation the browser renders. The unified run-completer's per-turn
  live-sync (ADR-0028) is gated on a disk persister, so removing the run-end
  append (#1203) left those configurations with an empty UI conversation and
  the browser's final-render POST rendered an empty bubble — the
  streaming-markdown final-render tests failed with "assertion never passed".
  The run-end sync is restored for the persister-less path only: it updates the
  last assistant message in place when one exists (preserving the components /
  quick replies tool execution attached to it) and suffix-dedups against the
  per-turn live-sync, so configurations with a persister stay duplicate-free.
  (#1217)

- Multi-turn UI runs no longer duplicate the final assistant message when the
  run ends. The run-end append of the run's accumulated stream buffer is gone:
  each completed turn — including the final one — reaches the UI conversation
  exactly once through the unified completion path's per-turn live-sync
  (ADR-0028), so the old suffix-match append-dedup hack is removed. This also
  fixes runs that stop at the max-turn limit, which previously appended the
  entire accumulated conversation a second time alongside the limit notice.
  Sub-agent child sessions append their transcript exactly once as before.
  (#1203)

- Multi-turn UI runs no longer drop intermediate assistant messages from the
  live chat view. With each completed turn now committed as its own message
  (ADR-0028 per-turn live-sync), the run's `done` final render emits a separate
  bubble for **every** assistant turn since the last user message — not just the
  last one — so prose the agent wrote in earlier turns (breakdowns, intermediate
  answers) stays visible without needing a page refresh, which previously
  re-rendered the full history and made the missing turns reappear.

- `scripts/agent-loop.sh` now extracts real batch session IDs for the per-issue
  trail: it parses the JSON slog attribute from the run's stdout
  (`"session_id":"<id>"`) instead of a bare `session_id=<value>` that never
  matched, so the dispatcher reports a genuine `~/.eitri/sessions/<id>/` route
  per stage instead of a misleading "no session_id found". (#1214)

- `collect` no longer fails with `unknown task_id` for a sub-agent that finished more than ~30s earlier and whose in-memory record was reaped. A completed sub-agent's result is now kept durably (surviving the record's TTL reap), and a `collect` issued later — e.g. after the parent paused for user input while the sub-agent finished — returns the completed status, result text, and turn count instead of failing and forcing a re-delegate. Running tasks still block until completion, genuinely unknown task IDs still error, parent cancellation still cascades to cancel in-flight sub-agents (collected as `cancelled`), and parallel done-then-waited results are still preserved. (#1200)

- The `browser` tool no longer closes tabs when a run ends. `EndSession` previously cancelled the session's chromedp allocator/target contexts, which in chromedp cascades to `Target.closeTarget` and closes every tab the session's `browser` tool had opened (both tabs it created and pre-existing tabs it attached to via `WithTargetID`). It now only forgets those CDP contexts without cancelling them, so browser tabs stay open across run/session boundaries — matching the very real scenario of auditing a tab that the session opened. Trade-off documented in the code: the CDP websocket stays connected for the lifetime of the run's tool instance instead of being torn down. the synthetic, empty placeholder user card it inserted before every assistant turn — when no user message could be attributed to a turn (e.g. after compaction or a turn with no preceding prompt), that card rendered as an empty one-line card that read as noise. Only user cards with real, matched content now render. (issue #1160)

- The sidebar can now be resized repeatedly: the first drag previously set `--sidebar-width` inline on `#app`, but the resize handle lives outside `#app` (appended to `body`) and only inherits the variable from `:root` — so after the first resize the handle stayed pinned at the original width and a second drag at the sidebar edge did nothing. The width is now set on `<html>`, which both the layout and the handle inherit from. A browser regression test (`TestBrowser_SidebarResizeHandleFollowsWidth`) drags the handle twice with real mouse events and asserts the sidebar resizes both times.

- The chromedp browser E2E tests now wait on observed state instead of fixed `time.Sleep` waits for background/DOM conditions, so a busy CI runner no longer makes them flake. Top-level fixed waits for streaming, tool-entry, undo-toast, overlay-close, theme-apply, and run-status conditions were converted to `pollForCondition` (the deterministic polling helper in `browser_test.go`), and the waits that genuinely assert on wall-clock time (streaming-responsiveness frame-gap measurement, live-tool-timer ticks, the escape-must-not-fire negative assertion, and slow-fake-server pacing) are now explicitly commented as deliberate pacing sleeps. `docs/TESTING.md` documents the polling-vs-pacing policy. (#1125)

- A missing or corrupt active persona now falls back to the built-in **generic persona + its prompt** (which honours the settings prompt) with a logged runtime warning, instead of reverting to a bare built-in constant (`history.DefaultSystemPrompt`) that ignored the settings override. Sub-agent missing/corrupt-persona fallback already resolved to `generic`; the UI and batch run paths now use the same generic-persona-first fallback. The settings save path also mirrors the prompt into `generic.yaml`, so the two always agree. (issue #1141)
- Switching back to a session whose run is still active no longer shows already-committed assistant messages twice: the server now marks every SSE history-replay event with `replayed` (on the per-subscriber copy only), and the streaming island skips replayed `token`/`component` events — committed bubbles are already rendered by the server on page load, so re-accumulating their tokens into a fresh streaming bubble produced a duplicate of the message. Live tokens after the replay continue to stream normally, and tool cards/thinking still rebuild from replay as before. A browser regression test (`TestBrowser_NoDuplicateMessageOnMidRunRejoin`) reloads a session mid-run with a committed bubble and asserts the message appears exactly once.
- Batch mode (`eitri -b`) now uses the full prompt from the remaining CLI arguments instead of silently dropping everything after the first word: `eitri -b implement feature X` runs the agent with the prompt "implement feature X" (Go's `flag` package stops parsing at the first non-flag argument, so the leftover args are joined with the `-b` value). `eitri -b ""` and `eitri -b "   "` now exit with a clear "empty prompt" error and non-zero exit code instead of falling through and starting the full UI server. Single-argument usage (`eitri -b "one prompt"`) is unchanged. (#1094)
- The CSS button system no longer relies on `!important` to resolve the specificity war between the generic `button`/`.form-actions` rules and the `.btn` hierarchy classes (`.btn-primary`/`.btn-danger`/`.btn-secondary`). The generic defaults are now zero-specificity `:where()` rules, so the hierarchy classes win by structure on any element (`<button>`, `<a>`, `<span>`) — including inside form action bars and the directory-browser dialog, whose overrides were previously being silently defeated. Send/Stop toggles, the session-close hover reveal, and child-session indent/radius were restructured the same way. The stylesheet's `!important` count drops from 32 to 13, and every remaining usage is a commented vendored-library override (Prism light palette, `pre.mermaid`); a regression test (`TestEmbeddedCSSButtonHierarchyNoImportant`) locks the budget in. Button appearance is unchanged except the directory-browser secondary buttons, which now show their intended filled style. (#1079)
- The lazy loader (`eitri-lazy-load.js`) now handles on-demand script-load failures gracefully (offline, blocked request, server hiccup): a rejected fetch of mermaid/KaTeX/Prism is caught instead of surfacing as an unhandled promise rejection in the console, logged exactly once per library (subsequent HTMX swaps do not re-attempt the fetch or re-log), and surfaced to the renderer islands via `eitri:mermaid-load-failed` / `eitri:katex-load-failed` / `eitri:prism-load-failed` events. Content degrades visibly instead of silently losing the diagram/formatting: diagrams fall back to their raw source under a "Diagram renderer could not be loaded. Raw code:" message, equations keep their raw LaTeX marked with a `.math-error` hint, and code blocks keep their raw text marked with a `.code-error` warning border. (#1078)
- All frontend islands now resolve the active session ID from the URL through one shared helper (`eitri-session-id.js`, `window.eitriGetSessionId()`), so sessions whose IDs contain `-` or `_` (not just hex) auto-connect their stream and resolve in the events/streaming islands exactly like hex IDs. The streaming and events islands previously used a hex-only regex and silently failed to auto-connect non-hex sessions; the composer, context, events, and streaming islands no longer parse session IDs themselves. A unit test (`TestSharedSessionIdHelper`) pins the shared regex across all four islands, and a browser test (`TestBrowser_NonHexSessionIDAutoConnect`) verifies a non-hex session ID auto-connects its stream on page load. (#1077)
- The streaming island (`eitri-stream.js`) no longer emits debug `console.log` output during normal operation: the disconnect-trigger log on HTMX swaps and the full packet dumps when rendering components are removed, so streaming, rendering, and disconnect flows stay silent. Error handling still uses `console.warn`/`console.error` on failure paths only. A regression guard (`TestStreamIslandNoDebugLogging`) fails if any `console.log` call is reintroduced. Streaming behavior is unchanged. (#1076)
- The persona selector dropdown (`eitri-persona-selector`) is now fully keyboard-operable: it implements the WAI-ARIA listbox pattern with a roving tabindex — ArrowUp/ArrowDown/Home/End navigate the options (wrapping at the ends), Enter/Space activate the focused option, Tab closes the widget, and Escape closes it and returns focus to the trigger. ArrowUp/ArrowDown on the closed trigger open the listbox so navigation starts immediately, and activating a persona hands focus back to the re-rendered trigger so keyboard users can keep operating the dropdown. The trigger advertises the popup via `aria-haspopup`/`aria-expanded`/`aria-controls` (pointing at the `persona-listbox`), the listbox is labelled, and each option exposes its selection state via `aria-selected`. (#1074)
- Streaming replies are now announced to screen readers: the ~80ms-mutated streaming bubble is deliberately *not* a live region (re-rendering the tail each flush would make assistive tech re-read the whole reply at token cadence), and instead a visually-hidden `role="status"`/`aria-live="polite"` region receives only the *new* text deltas of the reply, throttled to one announcement per second and flushed on run completion or error — so a screen-reader user hears the reply arrive in chunks without the full stream being re-read every 80ms. Task-list checkboxes in rendered markdown are now wrapped in a `<label>` with their item text (both in the server-side goldmark renderer and the client-side streaming `lightweightMarkdown`), so they are announced as "text, checkbox" instead of unlabelled checkboxes. Streaming flush performance is unaffected: the announce delta bookkeeping is O(delta) per flush and never copies the whole accumulated buffer. (#1071)
- The streaming island lifecycle is hardened: a run now finalizes exactly once per run (duplicate or replayed `done` packets within the SSE retention window are ignored via a run-status guard, instead of re-entering the final markdown render), tool-card elapsed timers are stopped the moment their cards leave the DOM (the interval self-stops when its card is removed, and `clearToolActivity`/`resetActivityTracking` stop timers before clearing state — previously `resetActivityTracking` dropped the timer map without calling `clearInterval`, leaking intervals), and a transient EventSource `onerror` during an active run no longer clears tool activity/thinking/elapsed data — tool cards keep their replay-stable keys (`turn`+`tool`+`args`), so after reconnect the replayed history resumes the same cards instead of creating duplicates, and replayed `tool_result` packets keep the originally recorded final elapsed instead of inflating it with wall-clock time spent disconnected. The existing `no_active_run` reconnect-storm suppression is unchanged. (#1070)
- Frontend custom elements no longer leak document/body-level listeners across HTMX re-renders: the context panel (`eitri-context`) now implements `disconnectedCallback` that removes its `document.body` `htmx:afterRequest` listener and the sidebar-header click listener (previously re-added on every swap and never removed, so each navigation pinned the detached panel in memory forever), and guards `connectedCallback` re-entry with an `_initialized` flag so moving or re-rendering the element cannot stack handlers. The persona selector (`eitri-persona-selector`) replaces its per-element permanent `document` click listener with a single delegated listener registered once at module scope that walks the current DOM — re-created selectors no longer accumulate closures over detached elements. The composer's `requestAnimationFrame` retry loop in `connectedCallback` is now bounded (`MAX_COMPOSER_INIT_ATTEMPTS`) and bails out if the element is torn down mid-retry, so a permanently missing form can't leave an endless rAF loop. (#1069)
- The CSS design token system is now complete and enforced: twelve custom properties referenced by components but never declared (`--muted`, `--border-muted`, `--danger`, `--text-secondary`, `--bg-secondary`, `--bg-tertiary`, `--fg`, `--fg-muted`, `--mono-font`, `--radius`, `--surface-hover`, `--composer-height`) are now declared in both theme token roots, restoring the intended styling of the persona page, the sessions table, form errors, and typing dots. Stream status colors, tool states, sandbox badges, hover colors, overlays, and surface tints are promoted to semantic tokens used consistently in dark and light themes, and every `rgba`/hex tint derived from a token (user-message tint, context-warning tint, focus rings, termination chips, glow pulses) is now computed via `color-mix()` from the token — so no dark-theme colors leak into light mode. A new CI lint test (`TestEmbeddedCSSNoBareHexOutsideTokenRoot`) fails on any bare hex/rgba color outside the token root declarations (Prism's fixed syntax palette is the sole exempt surface). (#1068)

- Chat bubbles no longer render tool messages or empty assistant tool-call turns: the live-history sync (per-turn conversation update) puts tool results — including the `collect` result carrying a sub-agent's full response — into the session conversation, and the chat view was rendering every non-assistant message as a user bubble with the user's own avatar. Tool messages and empty tool-call turns now stay out of the conversation (they are shown as sidebar tool cards during the run), so sub-agent responses no longer appear in the main view as if written by the user. Quick-reply chips on empty assistant messages still render.

- The chat-orphan regression test for a failed run start (`TestChatOrphanedMessageOnStartRunFailure`, issue #972) is restored: it now points at a reachable mock provider that passes live config validation (model discovery succeeds), then fails `buildLLMService` on an empty API key so the run start fails synchronously after a successful config save — replacing the unreachable-URL trick that config validation now rejects. (#1025)

- Persona home-directory resolution now runs through an explicit per-server seam (`ServerConfig.HomeDir` / `RunConfig.HomeDir`) instead of reading the process `HOME` env var at every lookup, and api test helpers inject a per-server temp home dir instead of mutating the global `HOME` — parallel api tests no longer race on persona resolution and the suite is stable under `make test-race`. Production behavior is unchanged. (#1023)
- The HTTP server now sets `ReadHeaderTimeout` (5s), `IdleTimeout` (60s) and a 1 MiB `MaxHeaderBytes` cap, so a client that connects and stalls mid-header (slow-loris) or leaves dead keep-alive connections behind is closed promptly instead of pinning a goroutine and socket indefinitely. Streaming responses are deliberately exempt from read/write deadlines, so long-lived SSE streams (chat stream + browser events stream) keep working exactly as before. (#966)- Token and thinking deltas are now batched server-side before being sent over SSE (flush every ~50ms or every 4096 chars, plus on event-type/turn changes and stream close), so a streaming run sends far fewer network frames while the client still receives the complete text. Run-state SSE history is now bounded by event count and a byte budget for high-volume token content, so a long reasoning stream no longer grows the in-memory history without bound and a subscriber reconnecting mid-run replays only the recent tail. (#967)
- `web_fetch` now reads the response body through a bounded limit (content cap plus a slack margin) instead of pulling the whole page into memory, so a huge or hostile page stops being read cleanly at the cap and the existing 32 KiB truncation still applies. Redirects are now capped (10) via `CheckRedirect`, so a redirect loop fails with a clear error instead of being followed indefinitely. (#953)
- `grep` with `context` now enforces the 2 KiB output cap during the workspace walk — the same per-line byte accounting used for `context: 0` — instead of accumulating every match and truncating only at render time, so context output can no longer exceed the cap or behave inconsistently. Files without matches are no longer retained in memory during the walk, and context windows are merged around matches as files are scanned instead of caching whole files for later rendering. (#952)
- `browser.click` now initializes through the deadline-bounded `prepareTarget` path like the other browser actions, so a hung CDP connection during `click` returns within the browser action timeout instead of blocking the agent loop indefinitely. The per-element wait/click timeout (10s) is applied as a child of the prepared operation context. (#951)
- LLM tool-call replay now replaces provider-generated opaque tool IDs with stable safe IDs, preventing GitHub Copilot Responses errors like `tool use id ... is invalid` on the next turn.
- LLM tool-call replay now repairs repeated streamed JSON argument chunks and collapses duplicate tool IDs before dispatch, preventing GitHub Copilot tool calls from failing with `invalid character '{' after top-level value`.
- Fix browser page becoming unresponsive during long streams by rendering streaming markdown incrementally instead of re-rendering the whole message on every flush.
- Reduce main-thread jank during streaming: very large single growing blocks (e.g. big code blocks) are now streamed append-only instead of re-rendered from scratch on every 80ms flush; the full-width header no longer uses expensive `backdrop-filter` blur that repainted on every scroll; auto-scroll is debounced, instant (not smooth-animation-stacked) while streaming, and holds position when the user has scrolled up; and the streaming-bubble relocation walk now runs only after an actual HTMX swap rather than on every token.

- Fix the live Thinking panel freezing the UI on long reasoning streams: the panel kept the whole (unbounded) reasoning transcript as one growing text node, forcing the browser to re-wrap/re-layout it on every frame during streaming — O(n²) main-thread layout that made the page unresponsive (gear/nav unclickable, Chrome "kill page"). The live transcript is now bounded to a trailing window while auto-scrolling, keeping the UI responsive.

- Fix the gear/header (global nav) becoming unclickable while a blocked-read confirmation is pending mid-run: the full-screen confirmation overlay (z-index:1000) covered the header (z-index:100), freezing the UI during a running/streaming session. The header now stacks above the overlay so nav stays usable while a confirmation waits.

- The confirmation modal now restores focus to the element that had it before the modal opened (typically the composer input) when it closes, and the undo toast moves focus to its Undo button as soon as it appears — so keyboard users always know where focus is after the allow/deny flow. Escape while the undo toast is showing no longer re-runs the deny action, which would respawn the 5s auto-close timer and leak the old one. (#1067)

### Documentation

- **CONTEXT.md**: compress the four Provider-flavoured domain glossary entries (`Provider`, `Provider endpoint`, `Model`, `Unverified model`) into a single `Provider` entry with compact inline bullets for endpoint/model/unverified, plus a link to the `internal/provider/` section of `docs/ARCHITECTURE.md`. (#1009)
- **CONTEXT.md**: strip the built-in tool name list from the `Tool` domain glossary entry, leaving a one-liner that links to the `internal/tool/` section of `docs/ARCHITECTURE.md` (via the `#built-in-tools` anchor). (#1012)
- **CONTEXT.md**: merge the `Context update` glossary entry into `Context panel` — the panel entry now references `runstate.ComputeContext()` and links to the `internal/runstate/` section of `docs/ARCHITECTURE.md` for the `context_update` SSE event contract and per-category token fields. (#1011)
- **CONTEXT.md**: trim the Bash tool domain glossary entry to a one-liner with links to the `internal/sandbox/` and `internal/tool/` sections in `docs/ARCHITECTURE.md`; sandbox and execution details now live only in `docs/ARCHITECTURE.md`. (#1006)
- **CONTEXT.md**: fix domain glossary and project structure drift — drop OpenRouter from supported providers, add `delegate`/`collect` to the Tool entry, correct the compactor config key names and note salience-aware compaction, drop removed DiffCard from Render component, and add `internal/message/` + `browser` tool to the project structure. (#960)

## [0.1.6] — 2026-07-29

### Added

- Settings now uses save-only config drafts with dirty-state Save/Revert controls and navigation/unload warnings for unsaved changes.
- Settings now always shows editable Provider endpoint controls with provider-default indicators and reset-to-default support.

### Fixed

- Settings Test Connection now validates the unsaved draft, reports discovered model count and selected-model availability, refreshes the model picker, and never writes the saved config.
- Provider switch in Settings now gracefully returns empty model list instead of
  error toast when saved credentials don't match the new provider. Model refresh
  saves the credentials first, then discovers models. (#948)


- Config + Settings UI for `browser_ws_url`: new config field with default `ws://127.0.0.1:9222`, merge handler, and text input in the Settings page with help text. (#918)
- Browser tool: implement `type` action — types text into an element identified by CSS selector. Clears existing value first, handles empty text as no-op, and returns clear error messages for invalid/missing selectors. (#919)
- PWA enablement: Eitri is now installable as a standalone desktop app. Adds `manifest.json`, service worker (`sw.js`), PWA icons derived from `face.webp`, and meta/apple-touch-icon tags in `<head>`. (#945)
- App shell caching: service worker precaches all static assets (`/static/*`) on install, uses cache-first strategy for static assets, network-first for Google Fonts, network-only for `/api/*` and SSE `/stream` endpoints, and falls back to cached app shell for navigation requests when offline. Provides instant loads on repeat visits and offline resilience. (#946)

### Fixed

- `grep` with context now applies the 2 KiB cap to final rendered output instead of double-counting match bytes and truncating early.

- Settings model refresh and Test Connection now use current unsaved form values while preserving masked saved API keys.
- Settings Save now validates the whole draft atomically, rejects unavailable selected models without changing saved config, supports first-time no-model saves, and notes that active runs keep previous settings.
- Settings Save now leaves saved provider config untouched when draft provider validation fails.

- `write` tool now accepts empty `content` values, enabling empty file creation and truncation while still rejecting omitted content.

- Session screenshot file serving now requires the owning `browser_id` cookie and rejects non-screenshot filenames.

- Workspace directory browser and workspace update endpoints now require the owning `browser_id` cookie, returning 401 when missing and 404 when mismatched.

- Fatal agent run errors now move sessions to `error`, persist that status in snapshots, and notify the browser sidebar.

- LLM stream close helper now exits after normal stream completion instead of leaking a goroutine until process exit.

- GitHub Copilot `gpt-5*` models now expose Thinking Level choices in Settings and preserve selected reasoning effort.

- Provider-switch model refresh now passes the selected provider to the server so models are discovered for the correct provider, not the previously saved one.

- Duplicate assistant bubbles when a run produces no text output (e.g., tool-only run) — the render handler now checks that the last assistant message was created after the triggering user message before rendering it.

- GitHub Copilot provider now calls root `/chat/completions` and sends Copilot-required headers during LLM requests. Previously Eitri hit `/v1/chat/completions` and omitted Copilot auth context headers, causing `404 page not found` errors on simple prompts.

- GitHub Copilot provider now discovers and runs `/responses`-only models like `gpt-5.5`. Eitri stores the selected model API mode at config save time and routes Copilot GPT models through root `/responses` when required, while keeping `/chat/completions` for chat-compatible models.

- OpenCode Go provider: ensure `/v1` suffix in base URL for OpenAI-compatible models. Previously, a bare `/zen/go` base URL produced `.../zen/go/chat/completions` (404), missing the required `/v1/` segment.

## [0.1.5] — 2026-07-26

### Added

- **Feed provider Usage data into CalibrationStore**: After each streaming LLM response, the agent loop now extracts `Usage.PromptTokens` from the stream's Done event, computes the chars-per-token ratio from the actual input text, and feeds it into the `CalibrationStore` for per-model exponential moving average calibration. Calibration changes are logged at Debug level. Supports both streaming (`ChatStream`) and non-streaming (`Chat`) paths. (PR #832)

- **Compactor: compact oversized user & assistant messages**: The compactor now scans all message roles (user, assistant, tool) instead of only tool results. A new `MessageSizeThreshold` control (configurable, default 2000 estimated tokens) gates which individual messages are eligible for compaction. Role-appropriate summarization prompts are used for each role. Assistant messages retain their `ToolCalls` after compaction. Compacted non-tool messages are tagged with `[MESSAGE COMPACTED]` prefix to prevent re-compaction. New config field `compaction_message_size_threshold` and `EITRI_COMPACTION_MESSAGE_SIZE_THRESHOLD` env var.

- **Load historical session from disk**: New `SessionManager.LoadFromDisk()` method restores a previously-persisted session snapshot into the in-memory session manager with status forced to idle. New `POST /api/sessions/{id}/load` endpoint triggers loading from disk; responds with an HTMX sidebar swap and redirect to the loaded session's chat view. Returns 404 if the session doesn't exist on disk. No-op redirect if the session is already active. Underlying `RunService.LoadSessionFromDisk()` coordinates disk read, UI session restoration, and conversation history rehydration.

### Fixed

- `make test` no longer deletes `~/.eitri/personas`. Persona test `TestMain` now overrides `HOME` to a temp dir instead of calling `os.RemoveAll` on the real user home directory.
- **`generic` persona location**: the built-in `generic` persona is now
  created in `~/.eitri/personas/` (user-level home) instead of
  `<workspace>/.eitri/personas/`, so it is shared across workspaces and
  not duplicated in each project.

### Changed

- **TurnCompleter interface**: extracted the ~95-line `OnTurnComplete` closure
  passed to `loop.RunAgent` into a proper `TurnCompleter` interface. `RunService`
  implements the interface. Compaction logic shared between auto-compaction
  (turn callback) and manual compaction (`CompactSession`) via the new
  `compactSessionHistory` helper. (#781)

### Removed

- **glob tool**: removed (covered by `bash` + `find`). (#733, #737)

### Added

- **Persona-driven system prompt**: active persona's system prompt is now
  used as the base for runs. A persona selector in the chat top bar lets
  users switch persona mid-session. The existing `system_prompt` config
  field acts as a session override on top of the persona's prompt. (#755)
- **Per-persona skill injection**: personas can declare injected skills
  that are automatically loaded when the persona is active. Deduplication
  prevents conflicts with manually activated skills. (#756)
- **Subagent persona support**: the `delegate()` tool accepts an optional
  `persona` field. Subagents can be spawned with any persona from the
  user's catalog, enabling role-specific subagents while the parent stays
  on another persona. (#757)
- **Persona CRUD**: users can now define named personas (system prompt +
  optional injected skills) in Settings → Personas. Personas are stored
  as YAML files under `.eitri/personas/<name>.yaml`. The `generic` persona
  is auto-created on first run. Up to 10 custom personas enforced.
  See ADR-0018. (#754)
- **Backward compatibility & cleanup**: `active_persona` defaults to
  `"generic"` when unset. Generic persona auto-created on server startup.
  `system_prompt` config field preserved as session override. Settings UI
  updated to clarify its override role. (#758)

- **bwrap sandboxing for bash commands**: shell commands now run inside a
  bubblewrap sandbox by default (requires `bwrap` on PATH; falls back
  gracefully). Read-only root filesystem, writable workspace and `/tmp`,
  separate PID namespace. Network enabled by default. Disable via Settings
  or `"sandbox": {"profile": "none"}` in config. See ADR-0017.

### Documentation

- **README.md**: add Prerequisites section with bwrap install instructions
  and sandbox configuration reference.
- **ARCHITECTURE.md**: add `internal/sandbox/` module to the module map
  and update BashTool description to reflect sandboxing.

## [0.1.4] — 2026-07-24

### Added

- Session persistence: snapshots written after every agent turn to `~/.eitri/sessions/<id>/session.json` and `~/.eitri/history/<id>/history.json`; restored on server startup; deleted on session delete. Persister with graceful Flush shutdown, atomic writes, and 1 GiB retention cap. (#702, #705, #708, #711)
- Gravatar support: `UserEmail` config field with Settings UI profile section; user chat bubbles render Gravatar avatar (MD5 hash, `d=mp` fallback, 32×32). (#671, #672, #683, #684)
- Debug config fields `DebugPrompt`, `DebugRequest`, `DebugLLMDir` in config, migrated from ad-hoc `os.Getenv` calls. (#696)
- Runner sub-package extraction: `runner/runconfig`, `runner/broadcast`, `runner/adapters`, `runner/loop` — clearer module boundaries. (#695, #699)
- `RunOpts` struct replacing `AgentConfig`, `RunSpec` struct, and `RunPlanner` seam for cleaner agent loop setup. (#642, #647, #648, #654)
- CI stability: increase Chrome websocket timeout to 60s, add retry logic to browser startup in tests, and remove duplicate browser test run from `release-check` Makefile target. (#655)
- Comprehensive test coverage: LLM error handling/stream parsing, provider profiles/auth, handler suites (confirm, sessions, skills, config, workspace, debug), runner service methods, tool infrastructure, copilot device flow, runstate SSE writers, and concurrency-safe run tracker/broadcast/subagent stores. (#657, #660, #663, #665, #670, #675, #676, #678, #680, #681, #687)
- HTTP trace recorder gains a dedicated `lastFailingTrace` slot that preserves the most recent non-2xx response (or errored request) — never evicted by the ring buffer. Crash dumps include this as `failing_http_trace` in `crash.json`. `HTTPTrace` gains a `ResponseHeaders` field capturing response headers for provider-side correlation. (#604)

### Fixed

- Assistant chat bubbles no longer stretch to the full messages container width. `.message` is now capped at `max-width: 90%` so wide content (long unbreakable lines, full-width tables) cannot push the bubble background and border past the readable area. Regression test `TestBrowser_AssistantBubbleMaxWidth` covers this.
- SSE stream no longer crashes when LLM returns tool call with empty arguments (e.g. hallucinated tool name). Empty `json.RawMessage` is now sanitized to `{}` before marshaling. (#605)
- Error toast modal X button now correctly closes the overlay. (#599aa38)
- Data race in `browserBroadcaster` between `Unsubscribe` and `Broadcast`. (#687)
- Workspace indicator now shows the session workspace instead of the server launch workspace. (#e86f8b3)
- Workspace update handler now accepts URL-encoded form data. (#5e0f6c7)

### Removed

- `DiffCard` and `FileEditCard` render components (and their LCS diff engine) — edit tool results now display inline in the tool card text output. (#700)

### Changed

- Runner package refactored from a monolith into four sub-packages: `adapters`, `loop`, `runconfig`, `broadcast`. (#695, #699)
- Debug configuration migrated from ad-hoc `os.Getenv` calls to structured `config.Config` fields. (#696)

## [0.1.3] — 2026-07-23

### Fixed

- Debug API `run` object now includes `busy`, `turns`, `pending_approval` fields; SSE diagnostic counters preserved as sibling fields (#585)
- Lock ordering in debug session handlers: snapshot SSE counters atomically under RunService.mu to avoid potential data race with concurrent run cancellation (#589)
- Response card duplication when run completes: EventSource no longer reconnects after receiving the "done" event (RENDERING state now treated as terminal in onerror handler). Also sets a no-active-run timestamp after cleanup to prevent autoConnectOnPageLoad from reconnecting stale sessions. (#N/A)

### Added

- Debug API: expose SSE event history in session debug endpoint (#565)
- Debug API: add `GET /api/debug/sessions/{id}/http` route as path-based alias for session-scoped HTTP trace lookup (#586)
- Debug API: session, runtime, and config endpoints (#556)
- Debug API: SSE subscriber/replay counters (#566)
- Perf: tool definitions computed once per run instead of every turn (hoist tool defs out of agent loop) (#551)
- RunService.ActiveRunCount() method for debug introspection
- Crash dumps: batch mode failure writes structured crash dump (#559)
- Crash dumps: WriteCrashDump() + RunService CrashDumpFunc wiring (#559)
- Crash dumps: UI mode triggers crash dump on fatal agent errors (#560)
- Crash dumps: agent loop panic recovery writes crash dump then re-panics (#560)
- doc.go files for the provider, litellm, skills, and tool packages

## [0.1.2] — 2026-07-23

### Added

- `prompt_cache_key` support for OpenAI-compatible providers (opencode_go, custom_openai) — sends session-scoped cache key to reduce repeated prefix processing on multi-turn conversations. (#553)
- Tool definitions are now hoisted out of the agent turn loop and computed once per run instead of every turn. (#551)

### Fixed

- SessionID prompt cache key no longer incorrectly gated by `ThinkingLevel`.

### Changed

- Pre-release version — superseded by v0.1.3 the following day with additional debug API, crash dump, and doc.go changes.

## [0.1.1] — 2026-07-22

### Fixed

- Session stream reconnect on page navigation: `autoConnectOnPageLoad` now
  always attempts connection instead of skipping when the rendered status is
  "idle". A time-based guard prevents reconnect storms after `no_active_run`.
  Also changes `handleChat` active-run 409 Conflict to 200 OK with
  `HX-Retarget` so the error toast is visible (HTMX drops non-2xx bodies).
  (#N/A)
- edit tool no longer dumps full file content as text blocks; returns concise summary with line change count. FileEditCard component uses snippet from args. (#538)

### Added

- Initial release infrastructure: VERSION file, `--version` flag, GitHub Actions CI + release workflows, versioned builds, multi-platform release targets, changelog, and release orchestration scripts. (#N/A)
- README.md with human-facing overview, installation instructions, configuration docs, and security notes.
- Changelog discipline policy documented in development flow — every behavioural change must have an Unreleased entry.

## [0.1.0] — 2025-07-18

### Added

- Initial public release of Eitri — a self-hosted, single-binary AI coding agent for Linux.
- HTMX + Templ chat UI with SSE streaming, tool cards, Mermaid diagrams, file diffs, and context panel.
- Agent loop with built-in tools: bash, glob, grep, read, write, edit, skill, delegate/collect, web_fetch, render_mermaid_diagram, render_quick_replies.
- Support for OpenCode Go, GitHub Copilot, and OpenRouter LLM providers via litellm transport.
- Agent Skills framework for modular system prompt extensions.
- Sub-agent support (nested agent loops via delegate/collect).
- Session management (in-memory, up to 10 concurrent sessions).
- Batch mode (`-b` flag) for headless issue processing.
- Browser E2E testing via chromedp.
- Provider discovery, authentication, and profile management.
- Architecture Decision Records (docs/adr/).
- Install script for Linux (scripts/install.sh).

[Unreleased]: https://github.com/glemsom/eitri/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/glemsom/eitri/releases/tag/v0.1.6
[0.1.5]: https://github.com/glemsom/eitri/releases/tag/v0.1.5
[0.1.4]: https://github.com/glemsom/eitri/releases/tag/v0.1.4
[0.1.3]: https://github.com/glemsom/eitri/releases/tag/v0.1.3
[0.1.2]: https://github.com/glemsom/eitri/releases/tag/v0.1.2
[0.1.1]: https://github.com/glemsom/eitri/releases/tag/v0.1.1
[0.1.0]: https://github.com/glemsom/eitri/releases/tag/v0.1.0
