# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

<!-- Categories: Added, Changed, Deprecated, Removed, Fixed, Security. Entries
     describe user-visible behavior; record dependency and compatibility
     changes under Changed. -->

### Added

- `eitri session stats [guid|all]`: aggregate per-session statistics — turns,
  input/output tokens, cache hit ratio, tool-call and error counts, wall-clock
  span — as one row per session or a labelled breakdown, with `--format json`.
- `eitri session tools [guid|all]`: every tool call joined to its result, with
  `ok`/`error`/`no-result` status, optional `--name`/`--turn` filters, a
  per-tool summary, and `--format json` carrying full arguments.

### Changed

- Bumped `charm.land/bubbletea/v2` to v2.0.10.
- Tightened the system prompt: reworked the read guidance into an
  explicit read-shape menu and trimmed duplicated or contradictory rules.

### Fixed

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
