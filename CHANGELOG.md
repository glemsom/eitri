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
- The batch `--format json` envelope now reports what a run cost as well as what
  it did: the run's `model`, its `duration_ms`, the `tool_calls` it dispatched
  and executed, and a nested `usage` object with the prompt, completion, and
  prompt-cache hit/miss/write token totals summed across the run's turns. Each
  run's envelope is now a self-contained ledger entry, so a fan-out supervisor
  can sum its children without reading N session transcripts.

### Changed

- The `skill` tool's `name` parameter now enumerates the model-visible skill
  names, so the tool offers exactly the skills the index lists; with no
  model-visible skill the tool stays registered and `name` is an unconstrained
  string.
- Skill activation is now derived from the conversation: a repeat activation of
  a still-active skill — including a second same-named call in the same turn —
  returns a short note instead of re-injecting its body, a body dropped by
  compaction can be re-activated, and a human `/skillname` counts as active. The
  activation mechanics moved out of the system prompt and into the `skill`
  tool's description.
- The skill index now shows up to 256 runes of a description instead of 100, and
  truncates at the last word boundary rather than mid-word. Descriptions commonly
  put their `Use when …` clause past the old cap, so the trigger was the part
  that got cut; the cost is a longer always-resident index line per installed
  skill.
- The builtin skill packs' descriptions now lead with the words a request
  actually uses — `fetch`, `download`, `offload`, `delegate`, `parallelize` —
  since that leading clause is all the index can carry.
- The builtin `subagents` skill now briefs the subagent as a fresh context —
  absolute paths, the goal, the answer's shape — caps the answer's length, sends
  bulky results to a workspace file, gives each task its own `EITRI_DIR` under
  `$TMPDIR/sa/N`, redirects stdin from `/dev/null`, raises the launching bash
  call's timeout, and on a failed envelope reads `.err`, retries once, then does
  that slice inline.
- The builtin `web-access` skill picks its path by content type (lynx for pages,
  raw fetch for JSON, `-o` for downloads) and names the failures that actually
  bite: bare `-s` hiding `--fail`'s error, a pipeline reporting the last
  command's status, `--fail` swallowing a 4xx body, HTML where JSON was
  expected, a JS-only page, and pagination.

### Removed

- The `dynamic_skill_catalog` config setting and its per-prompt lexical
  selector. The full skill catalog is always injected: keeping it byte-stable
  is what lets the provider read it from the prompt cache across turns, and a
  per-prompt subset broke that prefix. The setting defaulted off, so only an
  explicit opt-in is affected.

### Fixed

- Installing the latest stable release with `install.sh` no longer fails with
  `latest stable release has no versioned Linux amd64 archive`; the installer
  now reads the asset's `browser_download_url`, whose surrounding GitHub JSON
  nests an `uploader` object that defeated the previous pattern.
- Skill activation no longer truncates: the payload is delivered whole, exempt
  from the generic tool-result byte cap and matching the human `/skillname`
  path. The `skill` tool's description and already-active note now state that a
  skill is active whether the tool activated it earlier or the user invoked it
  with `/skillname`, and that the note appears only while the complete body is
  present.
- Skill discovery rejects a directory whose name begins or ends with a hyphen,
  matching the Agent Skills name rule, and surfaces the drop through the same
  warning path as an unparseable `SKILL.md` instead of accepting it silently.

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
