# Prompt-cache current status

**Current state: implemented conservatively; OpenCode Go cache behavior is blocked on provider-authorized validation.**

Eitri preserves one `x-opencode-session` value for every turn in a Session. For
OpenCode Go, it emits no `cache_control`, `prompt_cache_key`, or
`prompt_cache_retention`; no cache TTL, breakpoint, or hit/miss behavior is
claimed. This is the current code behavior, not evidence that the provider
rejects those fields.

The evidence and exact credentialed harness are in [OpenCode Go prompt-cache
contract evidence](opencode-go-prompt-cache-contract.md). Run that harness for
both documented dialects with provider credentials before selecting an
OpenCode-Go-specific cache policy or treating reported telemetry as validated.

## Telemetry and session records

Provider telemetry records cache reads as `prompt_cache_hit_tokens`, uncached
input as `prompt_cache_miss_tokens`, and, where the Responses wire reports it,
cache writes as `prompt_cache_write_tokens`. The persisted response-record
schema is documented in [Session debug transcripts](../sessions.md). A write is
separate from a hit; it does not establish later reuse.

## Downstream impacts

- `internal/provider` owns request extensions and usage parsing; it remains the validation gate.
- `internal/engine` forwards usage and `internal/tui/telemetry` presents the hit/miss ratio; neither proves provider-side caching.
- Completed [#58](https://github.com/glemsom/eitri/issues/58) reduces avoidable prompt growth, while completed [#93](https://github.com/glemsom/eitri/issues/93) consumes cache telemetry in the TUI. Neither ticket validates OpenCode Go caching; their statuses are unchanged.

The external and audit notes in this directory are retained as historical source
records. Their status banners link here rather than rewriting their attributed
findings.
