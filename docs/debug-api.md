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
- `run`: active run summary if one is in progress: `busy`, `turns`, `pending_approval`.
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
  - `sse_subscriber_count`: total distinct SSE connections created.
  - `sse_replay_count`: total times history was replayed.

This endpoint can produce a large response for long chats. For large payloads,
prefer the filtered `/api/debug/http?session_id=...` and/or limit the session
view with `?limit_messages=N`.

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
| `error` | string | Error message if the request failed |

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
source. There is no persistence layer — data is in-memory and lost on restart.

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

The recorder wraps the `http.Transport` used by the litellm adapters. It does
not modify request or response content — it copies body bytes into the ring
buffer without consuming the original stream.

Recorder does not record static assets, browser UI requests, or any
non-LLM-provider HTTP traffic.

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

- Traces are in-memory only — lost on server restart.
- No persistent event log or session timeline beyond message history.
- No deep debug toggle (always captures at full available detail).
- No rewind or other state-mutation endpoints.
- No pprof endpoints.
- No browser-client tracking.
