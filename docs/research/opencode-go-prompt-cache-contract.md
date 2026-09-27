# OpenCode Go prompt-cache contract evidence

**Status: BLOCKED — provider behavior has not been verified.** See [Prompt-cache current status](prompt-cache-status.md) for the implementation summary and downstream impacts.

## What is authoritative so far

OpenCode Go's public documentation requires clients to send a stable
`x-opencode-session` for each Eitri Session so OpenCode can optimize routing and
prompt caching. It does not document request-side `cache_control`,
`prompt_cache_key`, or `prompt_cache_retention`, cache TTLs or eviction, or
SSE/JSON cache-usage fields. The documented endpoint table defines the supported
wire dialects; it does not verify acceptance of undocumented extension fields.

Source: [OpenCode Go documentation](https://opencode.ai/docs/go/#how-it-works)
and its [endpoint table](https://opencode.ai/docs/go/#endpoints), checked
2026-09-27. The unauthenticated `GET /zen/go/v1/models` response was reachable
on that date, but supplied only ordinary model metadata and no endpoint or cache
capability metadata.

Therefore the only Eitri behavior supported by authoritative material is to
preserve one Session identity across all turns in an Eitri Session. The current
conservative Eitri implementation sends `x-opencode-session` and emits none of
`cache_control`, `prompt_cache_key`, or `prompt_cache_retention` to either
OpenCode Go dialect. This implementation choice is not provider verification
that those controls are rejected, ignored, or unavailable.

| Dialect | Documented client cache controls | Documented TTL/accounting | Current conservative Eitri behavior |
| --- | --- | --- | --- |
| Chat Completions | No controls documented | No TTL or cache-usage schema documented | Send Session identity only; emit no cache-control extensions |
| Anthropic Messages | No controls documented | No TTL or cache-usage schema documented | Send Session identity only; emit no cache-control extensions |

Provider acceptance, effect, permitted TTLs, and cache accounting for the
undocumented fields remain unverified.

## Downstream impacts

- `internal/provider` keeps the OpenCode Go extensions disabled; the provider-authorized harness is the gate for enabling them.
- `internal/engine` and `internal/tui/telemetry` may record reported cache usage, but it is not proof of OpenCode Go cache behavior until the harness is run.
- [#58](https://github.com/glemsom/eitri/issues/58) remains relevant as a completed prompt-size reduction, and [#93](https://github.com/glemsom/eitri/issues/93) as the completed cache-meter consumer; neither validates provider caching. Their closed status is unchanged.

## Repeatable provider-authorized harness

`TestOpenCodeGoPromptCacheContract` is the authorized seam. For each supported
wire dialect it constructs `provider.Request` values and calls
`provider.NewOpenCodeGo(...).Stream` for a cold turn and a repeated turn with
the same generated `SessionKey`. It asserts each outbound request uses the
dialect's documented route, preserves that Session identity in
`x-opencode-session`, omits `cache_control`, `prompt_cache_key`, and
`prompt_cache_retention`, and reaches terminal streamed usage with non-negative
reported token counts. It logs the redacted method, path, body, identity headers,
and terminal usage for the credentialed run. It deliberately runs only when given
explicit provider credentials and models:

```sh
EITRI_OPENCODE_GO_API_KEY=... \
EITRI_OPENCODE_GO_CHAT_MODEL=<documented Chat Completions model> \
EITRI_OPENCODE_GO_MESSAGES_MODEL=<documented Messages model> \
go test ./internal/provider -run '^TestOpenCodeGoPromptCacheContract$' -count=1 -v
```

Set `EITRI_OPENCODE_GO_URL` only to test a provider-authorized alternate Go
endpoint. Do not commit credentials or harness output containing credentials.

## Execution record — 2026-09-27

The authorized command was run with verbose output after safe credential
source discovery:

```sh
# credential values are never printed or persisted
EITRI_OPENCODE_GO_API_KEY=… \
EITRI_OPENCODE_GO_CHAT_MODEL=… \
EITRI_OPENCODE_GO_MESSAGES_MODEL=… \
go test ./internal/provider -run '^TestOpenCodeGoPromptCacheContract$' -count=1 -v
```

Both `chat` and `messages` subtests skipped because the required API-key and
per-dialect model environment variables were absent. No provider request was
made, so there is no provider-authorized wire identity, extension acceptance or
rejection, or cold/repeated usage outcome to record. The local recorder seam
was separately exercised with its non-provider fixture and passed; it verifies
redacted request recording and terminal-usage capture only, not provider
behavior.

Credential discovery found the supported Eitri config mechanism
(`EITRI_CONFIG`, resolving here to `~/.eitri/config.json`), but its
`opencode_go.key` was absent. The process environment had no required OpenCode
Go key or model variables. No credential value was read, printed, or written.

## Exact blocker and required action

No OpenCode Go credential or provider-authorized model selection is available
in this workspace. The harness is skipped rather than presenting localhost
fixtures, unauthenticated discovery, or native-provider documentation as
provider verification.

A credentialed OpenCode Go account owner must run the command above against one
currently documented model on each dialect and attach the verbose output (and,
if fields are absent, a provider support confirmation of the cache usage schema)
to this evidence. They must confirm whether `cache_control`,
`prompt_cache_key`, and `prompt_cache_retention` are accepted, ignored, or
rejected; the permitted TTLs; and cold/repeated cache
read/write accounting. Only then can Eitri select a dialect-specific cache
policy or alter its cache telemetry.
