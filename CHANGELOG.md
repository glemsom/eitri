# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

<!-- Categories: Added, Changed, Deprecated, Removed, Fixed, Security. Entries
     describe user-visible behavior; record dependency and compatibility
     changes under Changed. -->

### Added

- A model-facing `skill` tool: the agent activates a listed skill by name and
  receives its instructions and the absolute directory holding its bundled
  resources, instead of translating the request into a `bash cat` of `SKILL.md`.

### Changed

- Skill activation is now derived from the conversation: a repeat activation of
  a still-active skill — including a second same-named call in the same turn —
  returns a short note instead of re-injecting its body, a body dropped by
  compaction can be re-activated, and a human `/skillname` counts as active. The
  activation mechanics moved out of the system prompt and into the `skill`
  tool's description.

### Fixed

- Installing the latest stable release with `install.sh` no longer fails with
  `latest stable release has no versioned Linux amd64 archive`; the installer
  now reads the asset's `browser_download_url`, whose surrounding GitHub JSON
  nests an `uploader` object that defeated the previous pattern.

## [0.1.1] - 2026-10-17

### Added

- A run-end record in the message-layer transcript (`messages.jsonl`): every
  run now appends one `{"dir":"end","reason":…}` line naming why it ended
  (`completed`, `max_turns`, `stopped`, or `error`), so a run that looped to
  the turn cap, one that was stopped, and one that failed are distinguishable
  after the fact;
  `session show` prints it as a trailing `end=<reason>` line and
  `session stats` reports it as `outcome`.
- `eitri session stats [guid|all]`: aggregate per-session statistics — turns,
  input/output tokens, cache hit ratio, tool-call and error counts, wall-clock
  span — as one row per session or a labelled breakdown, with `--format json`.
- `eitri session tools [guid|all]`: every tool call joined to its result, with
  `ok`/`error`/`no-result` status, optional `--name`/`--turn` filters, a
  per-tool summary, and `--format json` carrying full arguments.

### Changed

- A turn's tool calls now run concurrently (bounded in flight), so a turn's
  wall-clock approaches its slowest call rather than the sum of the calls;
  tool results and events are still emitted and appended in the model's call
  order, leaving the next provider request byte-identical.
- The model-visible skill catalog is now one compact line per skill, grouped
  under a bracketed `[<root>]` heading so each root path appears once, replacing
  the per-skill XML and the repeated absolute path; descriptions are capped in
  width, and a skill may declare a `short-description` one-liner that the catalog
  shows in place of its full description (the full text still arrives on
  activation).
- New `dynamic_skill_catalog` config setting: when enabled, only the skills a
  deterministic selector ranks relevant to the prompt are injected, plus a note
  that more exist; it defaults off (the full catalog).
- Bumped `charm.land/bubbletea/v2` to v2.0.10.
- Tightened the system prompt: reworked the read guidance into an
  explicit read-shape menu, nudged toward issuing independent tool calls in one
  turn, and trimmed duplicated or contradictory rules.

### Fixed

- A provider stream that is reset mid-flight (`stream error: stream ID N;
  CANCEL; received from peer`, or any HTTP/2 peer reset, dropped connection, or
  transport error) is now retried as long as no content has been streamed yet,
  so a transient reset resumes the turn instead of failing it; a reset after
  content has started still surfaces, since a retry would duplicate output.
- Provider requests now retry `429 Too Many Requests`, honoring a
  `Retry-After` header in both its delta-seconds and HTTP-date forms with a
  bounded, jittered backoff; a value exceeding the remaining retry budget
  stops the loop, and other `4xx` responses still do not retry.
- Transcript write failures are counted and reported once at run end instead
  of being dropped silently.
- Session transcript reading now decodes each message's `tool_call_id`, so
  `session talk` tool-result labels and `session grep`'s `tool_call_id` matches
  work instead of silently reading an empty id.
- `bash` output compression now reports collapsed output instead of dropping
  it silently: runs of consecutive duplicate lines are replaced by an explicit
  `+N repeated lines collapsed` marker, alongside the existing truncation marker.
- `subagents` skill: a subagent no longer boots with a default config, a failed
  run no longer reads as an empty answer, and the handoff leads with piped stdin
  and states that subagent isolation depends on the launch directory.
- `web-access` skill: fetch recipes fixed through a pipe and on redirects, and
  curl's progress meter silenced on every fetch.

## [0.1.0] - 2026-09-27

### Added

- Initial public release.
