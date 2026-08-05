# Debug API

Eitri exposes a JSON debug API at `http://127.0.0.1:8080/api/debug`. It is
mounted on the same HTTP server as the browser UI.

The debug API is an operational tool for inspecting the live Eitri process:
active sessions, current run state, LLM provider HTTP traces (request/response
bodies), session history, and system configuration. It is designed to be
consumed both by humans (curl/jq) and by Eitri's own agent or an external
troubleshooting agent.

There is no separate authentication. Treat it as local developer tooling and
do not expose it on an untrusted network.

## Quick Start

```sh
BASE=http://127.0.0.1:8080
curl -sS "$BASE/api/debug" | jq .
```

The top-level response gives a summary of everything. Drill into specific
sections with query parameters or sub-routes.

```sh
# Full snapshot (session state + HTTP traces + history)
curl -sS "$BASE/api/debug" | jq .

# HTTP traces only
curl -sS "$BASE/api/debug/http" | jq .

# HTTP traces scoped to a session
curl -sS "$BASE/api/debug/http?session_id=abc123" | jq .

# All active sessions (metadata + message count + status)
curl -sS "$BASE/api/debug/sessions" | jq .

# One session with full message history
curl -sS "$BASE/api/debug/sessions/abc123" | jq '{
  session: .session,
  messages: [.messages[] | {role, content_length: (.content | length)}]
}'

# LLM provider HTTP traces for a specific session
curl -sS "$BASE/api/debug/sessions/abc123/http" | jq '.traces[:3]'

# Runtime info (active runs, memory)
curl -sS "$BASE/api/debug/runtime" | jq .
```

## Data Sources

The API reads state from three sources:

| Source | Description |
|--------|-------------|
| **Session Manager** | In-memory UI sessions (`Message`, `Status`, `ActiveSkills`, `Components`). Always available. |
| **RunService** | Active run state per session (`RunState` — cancel, done signal, SSE state). Only available while a run is in progress. |
| **HTTP Trace Recorder** | Bounded ring-buffer of LLM provider HTTP requests/responses. Available once at least one LLM call has been recorded. |

A session can exist in the session manager without an active run or recorded
HTTP traces. In that case the debug response omits those sections.

## Response Format

All responses use `Content-Type: application/json`. Errors carry an `"error"`
string field and an appropriate HTTP status code.

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Invalid input or query parameter |
| 500 | Internal error (recorder unavailable, session lookup failed) |

## Endpoints

### `GET /api/debug`

Top-level debug snapshot. Returns all available data in a single response.

```sh
curl -sS "$BASE/api/debug" | jq .
```

Response fields:

- `version`: Eitri version string.
- `up_since`: process start timestamp.
- `runtime`: short runtime summary (active run count, session count).
- `sessions`: list of session debug entries (see below).
- `http_traces`: global HTTP traces (all sessions, most recent first, max 20).
- `config_summary`: sanitized config summary (provider, model, context window —
  never API keys or secrets).

The body of each session debug entry includes:

- `id`, `title`, `status` (`idle`/`running`/`error`).
- `message_count`, `active_skills`.
- `run`: active run summary if one is in progress: `busy`, `turns`, `pending_approval`, `sse_subscriber_count`, `sse_replay_count`.
- `latest_http`: last 3 HTTP traces for this session.
- `last_message_timestamp`.

### `GET /api/debug/sessions`

List all sessions with debug summaries (no full history).

```sh
curl -sS "$BASE/api/debug/sessions" | jq '.sessions[] | {id, title, status, message_count, active_skills}'
```

### `GET /api/debug/sessions/{session_id}`

Full details for one session, including complete message history.

```sh
curl -sS "$BASE/api/debug/sessions/abc123" | jq '{
  session: .session | {id, title, status},
  message_count: (.messages | length),
  traces: [.http_traces[] | {timestamp, provider, status, duration_ms}]
}'
```

Response fields:

- `session`: session record (id, title, status, browser_id, timestamps).
- `messages`: full message history array. Each message has `role`, `content`,
  `reasoning_content` (if any), `created_at`, `components`, `quick_replies`.
- `http_traces`: most recent HTTP traces for this session (max 20).
- `active_skills`: skill names activated in this session.
- `run`: active run summary if running, includes:
  - `status`: current session status.
  - `busy`: whether the agent loop is actively executing a turn.
  - `turns`: turns consumed so far for the current run.
  - `pending_approval`: whether a tool is waiting for user confirmation.
  - `sse_subscriber_count`: total distinct SSE connections created.
  - `sse_replay_count`: total times history was replayed.
  - `sse_history`: recent SSE events broadcast during active run (max 50,
    omitted when idle or no active run). Each event has `type`, `kind`,
    `timestamp`, and event-specific fields.

This endpoint can produce a large response for long chats. For large payloads,
prefer the filtered `/api/debug/http?session_id=...` and/or limit the session
view with `?limit_messages=N`.

### `GET /api/debug/sessions/{session_id}/http`

Return recorded LLM provider HTTP traces for a specific session. This is an
alias for `/api/debug/http?session_id=...` using a path parameter instead of
a query parameter.

```sh
curl -sS "$BASE/api/debug/sessions/abc123/http" | jq '.traces[:3]'
curl -sS "$BASE/api/debug/sessions/abc123/http?limit=5" | jq '.traces | length'
```

Query parameters:

- `limit`: max traces to return (default 20, max 100).

Response shape is identical to `GET /api/debug/http` with `traces` and
`in_flight` arrays.

### `GET /api/debug/http`

Return recorded LLM provider HTTP traces. Supports session and provider
filters.

```sh
curl -sS "$BASE/api/debug/http" | jq '.traces | length'
curl -sS "$BASE/api/debug/http?session_id=abc123&limit=5" | jq '.traces[] | {timestamp, provider, status, duration_ms, request_bytes, response_bytes}'
```

Query parameters:

- `session_id`: filter to one session.
- `provider_id`: filter by provider (e.g. `opencode_go`, `github_copilot`).
- `limit`: max traces to return (default 20, max 100).

Each trace record:

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | string (ISO 8601) | When the request started |
| `session_id` | string | Session that triggered this LLM call |
| `provider_id` | string | Eitri provider ID |
| `method` | string | HTTP method (`POST`) |
| `url` | string | Request URL (path only for security) |
| `status` | int | HTTP status code from provider |
| `duration_ms` | int | Round-trip duration in milliseconds |
| `request_bytes` | int | Request body byte count |
| `request_body` | string | Truncated request JSON body (max 256KB) |
| `response_bytes` | int | Response body byte count |
| `response_body` | string | Truncated response JSON body (max 256KB) |
| `response_headers` | object | Provider response headers (map of header name to value arrays, e.g. `x-request-id` for provider-side correlation). Omitted when empty. |
| `error` | string | Error message if the request failed |
| `model` | string | Model name that produced the response (extracted from the request body or reported by the provider). |
| `attempt` | int | Zero-based retry attempt number of this call (0 = initial call). |
| `finish_reason` | string | Provider-reported finish reason (`stop`, `length`, `tool_calls`, `end_turn`, …). Omitted when unknown. |
| `usage` | object | Provider-reported token usage: `prompt_tokens`, `completion_tokens`, `total_tokens`, `reasoning_tokens`, `cache_read_tokens` (prompt cache hits) and `cache_write_tokens` (prompt cache creation) where the provider reports them. Parsed from the response body (including the stream tail when the body exceeds 256KB). Omitted when the provider returned no usage. |
| `ttfb_ms` | int | Time-to-first-byte in milliseconds — time from request start to the first response byte. |
| `error_class` | string | Structured capture-time error classification: `rate_limit`, `timeout`, `auth`, `context_length`, `network`, or `other`. Empty on success. |

Request and response bodies are diagnostic data. They may contain conversation
content, tool results, and file contents the agent processed. Do not assume
they are safe to share externally.

### `GET /api/debug/http/{trace_id}`

Return a single HTTP trace by its ID.

```sh
curl -sS "$BASE/api/debug/http/trace-abc123" | jq .
```

Returns `404` if the trace ID is unknown.

### `GET /api/debug/runtime`

Return process and runtime summary. Useful as a lightweight liveness check.

```sh
curl -sS "$BASE/api/debug/runtime" | jq .
```

Response fields:

- `version`: Eitri version.
- `up_since`: process start time.
- `active_run_count`: number of sessions with active runs.
- `session_count`: total UI sessions across all browsers.
- `recorded_http_traces`: total stored HTTP traces.
- `active_sessions`: per-session SSE diagnostic counters (omitted when idle):
  - `session_id`: session identifier.
  - `sse_subscriber_count`: total distinct SSE connections created for this session.
  - `sse_replay_count`: total times historical events were replayed.
- `config`: sanitized config (provider ID, model name, context window tokens,
  max turns). Never includes API keys or secrets.

### `GET /api/debug/health`

Minimal liveness probe. Returns `{"status": "ok"}`.

### `GET /api/debug/config`

Return current config for troubleshooting (secrets redacted).

```sh
curl -sS "$BASE/api/debug/config" | jq .
```

Response fields:

- `provider_id`: active provider.
- `model`: selected model name.
- `base_url`: provider base URL.
- `context_window_tokens`: context window cap (used for context panel
  estimates).
- `max_turns`: turn limit per run.
- `command_timeout`: per-command timeout in seconds.
- `has_api_key`: boolean — whether an API key is set (value never exposed).
- `completed_run_retention_ms`: how long (ms) a completed run stays in the
  active map, allowing SSE subscribers to replay historical events. Omitted
  when no RunService is configured.

### `GET /api/debug/metrics`

Aggregate per-provider/per-model LLM health counters, accumulated by the trace
recorder at capture time. This is the aggregate view of provider behaviour that
the raw trace ring-buffer cannot provide: counters survive ring-buffer
rotation, and error classes are classified once at capture time (from the HTTP
status code and the error observed then) — never by scanning error text at
display time. Batch (headless) runs and sub-agents feed the same counters as
browser runs.

```sh
curl -sS "$BASE/api/debug/metrics" | jq .
```

Response shape:

- `generated_at`: snapshot timestamp.
- `total_calls`: total LLM calls recorded across all providers/models.
- `total_errors`: total failed calls (non-2xx status or transport error).
- `providers`: array of per-provider groups, sorted by `provider_id`. Each
  entry has:
  - `provider_id`: Eitri provider ID.
  - `total_calls`: total calls for this provider.
  - `models`: array of per-model aggregates, sorted by `model`. Each entry has:
    - `model`: model name (from the request body).
    - `calls`: total calls for this provider+model.
    - `retries`: number of calls that were retry attempts (attempt > 0).
    - `errors`: error counts by structured class: `rate_limit`, `timeout`,
      `auth`, `context_length`, `network`, `other`. Every class key is always
      present.
    - `latency_ms`: latency histogram counts keyed by cumulative bucket label
      (`le_100`, `le_250`, `le_500`, `le_1000`, `le_2500`, `le_5000`,
      `le_10000`, `le_30000`, `inf`).
    - `tokens`: provider-reported token totals: `prompt_tokens`,
      `completion_tokens`, `cache_read_tokens`, `cache_write_tokens`,
      `total_tokens`.
    - `cache`: cache hit/miss counts. A call counts as a hit when the
      provider reported cached prompt tokens (`cache_read_tokens > 0`); a miss
      when measured usage was reported with no cached tokens. Calls without
      measured usage count toward neither.
    - `last_called`: timestamp of the most recent call (omitted before any
      call).
    - `last_error`: error class of the most recent failed call (omitted when
      none).

Example:

```json
{
  "generated_at": "2026-08-05T10:02:07Z",
  "total_calls": 42,
  "total_errors": 3,
  "providers": [
    {
      "provider_id": "opencode_go",
      "total_calls": 42,
      "models": [
        {
          "model": "deepseek-v4-flash",
          "calls": 42,
          "retries": 5,
          "errors": {"rate_limit": 1, "timeout": 1, "auth": 0, "context_length": 0, "network": 1, "other": 0},
          "latency_ms": {"le_100": 2, "le_250": 9, "le_500": 15, "le_1000": 9, "le_2500": 4, "le_5000": 2, "le_10000": 0, "le_30000": 0, "inf": 1},
          "tokens": {"prompt_tokens": 12450, "completion_tokens": 6230, "cache_read_tokens": 3100, "cache_write_tokens": 900, "total_tokens": 22680},
          "cache": {"hits": 12, "misses": 27},
          "last_called": "2026-08-05T10:02:07Z",
          "last_error": "rate_limit"
        }
      ]
    }
  ]
}
```

Returns `404` when no debug recorder is enabled (mirroring the other trace
endpoints).

## Data Flow

```
Browser / curl        api.Server
    |                     |
    |  GET /api/debug      |
    |--------------------->|
    |                     |
    |                     |-- session.Manager.All()      → session metadata + messages
    |                     |-- RunService.activeRun()     → run state per session
    |                     |-- debug.Recorder.Traces()    → HTTP traces
    |                     |
    |  JSON response       |
    |<---------------------|
```

The debug handler assembles the response inline by reading from each data
source. Traces and run timelines are also persisted to disk and restored on
startup (see [Persistence](#persistence)).

## HTTP Trace Recording

Every provider-bound LLM call passes through an HTTP trace recorder. The
recorder is a bounded ring-buffer:

- **Capacity**: 20 completed traces globally (oldest dropped when full).
- **Body limit**: 256KB per request/response body (larger bodies truncated).
- **Session scoping**: each trace is tagged with the `session_id` that
  triggered the request, enabling per-session filtering.
- **Active traces**: in-flight traces are tracked separately. They appear in
  responses with `"status": 0` and `"duration_ms"` representing elapsed time.
  They are moved to completed traces when the response finishes or fails.
  In-flight tracking is capped (64 by default): when the cap is reached the
  oldest in-flight trace is evicted with an `evicted` marker so the map stays
  bounded even if a response body is never read or closed.

The recorder wraps the `http.Transport` used by the litellm adapters. It does
not modify request or response content — it copies body bytes into the ring
buffer without consuming the original stream.

The completion callback (`OnComplete`) fires after the recorder mutex is
released and persistence itself is asynchronous, so recording adds no
trace-induced latency to the LLM request path and parallel sessions never
serialize on a disk write.

Recorder does not record static assets, browser UI requests, or any
non-LLM-provider HTTP traffic.

### Last failing trace

The recorder keeps a dedicated slot for the most recent non-2xx (or errored)
trace — a response with a status outside 2xx, or a transport-level error. This
trace is never evicted by the ring buffer, so the last failure is always
available even when the ring buffer has rotated past it. Crash dumps include it
as `failing_http_trace` (see crash dumps under the Eitri data directory).

### Persistence

Traces and run timelines are not limited to the in-memory ring buffer:

- **Traces** are written to
  `<data-dir>/sessions/<session_id>/traces/<trace_id>.json` on completion and
  are restored into the recorder on startup, so previously recorded traces
  remain queryable after a restart. Writes go through a bounded async worker
  (256 queued) so disk I/O never blocks the HTTP response path; if the queue is
  ever full, the trace is deferred and the shutdown flush persists it.
- **Run timelines** are written to
  `<data-dir>/sessions/<session_id>/timeline/<started_at>.json` at the end of
  each run (one condensed timeline file per run).
- Traces of permanently deleted sessions are not recreated — `SaveTrace` skips
  sessions whose `session.json` has been removed.
- The on-disk archive is bounded by a 1 GiB retention cap; the oldest timeline
  and trace files are pruned when the total exceeds the cap.

`<data-dir>` defaults to `~/.eitri` and can be overridden with the `EITRI_DIR`
environment variable.

## Common Investigation Recipes

### Find sessions with active runs

```sh
curl -sS "$BASE/api/debug/runtime" | jq '{active_run_count, session_count}'
curl -sS "$BASE/api/debug/sessions" \
  | jq '.sessions[] | select(.status == "running") | {id, title}'
```

### Inspect what was sent to the LLM

```sh
SESSION=abc123
curl -sS "$BASE/api/debug/sessions/$SESSION/http?limit=5" \
  | jq '.traces[] | {timestamp, status, duration_ms, request_body: (.request_body | .[0:500] + "..."), response_body: (.response_body | .[0:500] + "...")}'
```

### Compare two LLM requests in the same session

```sh
SESSION=abc123
curl -sS "$BASE/api/debug/http?session_id=$SESSION&limit=2" \
  | jq '.traces[] | {timestamp, request_bytes, status, request_body}'
```

The request body is a full OpenAI-compatible or Anthropic-compatible JSON
payload with `messages`, `tools`, `stream`, etc. Compare `messages` arrays
between consecutive requests to see how tool results were fed back.

### Check whether a session has an active run

```sh
SESSION=abc123
curl -sS "$BASE/api/debug/sessions/$SESSION" | jq '.run'
```

`null` means no active run. A non-null object means the agent loop is running
or completing.

### Find stuck or failed requests

```sh
curl -sS "$BASE/api/debug/http" | jq '.traces[] | select(.error != null) | {timestamp, session_id, error}'
```

### Troubleshoot a slow LLM call

```sh
curl -sS "$BASE/api/debug/http" \
  | jq '.traces | sort_by(.duration_ms) | reverse[:3] | .[] | {timestamp, session_id, duration_ms, request_bytes, response_bytes}'
```

## Implementation Notes

- The recorder is created during startup in `cmd/eitri/main.go` and injected
  into the LLM transport and the debug handler.
- The recorder must be thread-safe: recorded HTTP traces may arrive from any
  goroutine (one per active chat).
- The debug handler reads from `session.Manager`, `RunService`, and the
  recorder. It does not read the `config.Manager` directly for secrets — config
  fields are sanitized before returning.
- Message history for a session comes from `session.Manager.All()` or
  `session.Manager.Get()`. The response includes the full `Messages` array.
  The caller is responsible for pagination if needed via `?limit_messages=N`.
- HTTP trace body truncation at 256KB is a hard cap. Bodies larger than this
  have the last N bytes replaced with `... [truncated X bytes]`.
- The recorder's global cap of 20 traces is a compile-time default and can be
  tuned in the recorder constructor.

## Limitations (v1)

- Traces and run timelines are persisted to disk (bounded by the 1 GiB
  retention cap; see [Persistence](#persistence)) but the debug API itself has
  no query surface over the historical archive — only the restored in-memory
  recorder contents are exposed.
- No deep debug toggle (always captures at full available detail).
- No rewind or other state-mutation endpoints.
- No pprof endpoints.
- No browser-client tracking.
