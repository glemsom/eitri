# Eitri — Architecture Guide

> For AI agents navigating the codebase. Explains module boundaries, key types, data flow, and extension points.
> This guide is canonical for implementation topology, package boundaries, data flow, and extension seams.

## Overview

Eitri is a self-hosted, single-binary AI coding agent for Linux. It launches an HTTP server with an HTMX-based chat UI for Chrome on Linux. A browser profile can keep up to 10 in-memory chat sessions via top-bar tabs. Each session gets a tmux-managed shell session for command execution, initially rooted at the launch workspace (process CWD). No sandbox.

```mermaid
flowchart LR
    Browser["Browser (HTMX + Templ shell + islands)"]

    subgraph Server["cmd/eitri (HTTP + SSE server)"]
        API["api/ (routes, SSE)"]
        RunSvc["runner/ (RunService)"]
        Runner["runner/ (Manager - cache)"]
        History["history/ (session history)"]
        FileUtil["fileutil/ (file operations)"]
        Skills["skills/ (Agent Skills registry)"]
        Executor["executor/ (tmux)"]
        Config["config/ (JSON file)"]
    end

    Browser <-->|SSE + HTMX request/response| Server
    API --> RunSvc
    API --> Skills
    RunSvc --> Runner
    RunSvc --> History
    RunSvc --> FileUtil
    RunSvc --> Skills
    RunSvc --> Executor
```

## Module map

### `cmd/eitri/main.go` — Entry point

Orchestrates startup:
1. **Runtime audit** (`executor.RunAudit`) — verifies `tmux` binary on `$PATH`
2. **Workspace capture** — resolves process CWD as the launch workspace; v1 has no CLI workspace argument
3. **Config manager** (`config.Manager`) — reads `~/.eitri/config.json`
4. **Session manager** (`executor.SessionManager`) — manages per-chat tmux executor lifecycle; sessions are in-memory; tmux sessions start in launch workspace; startup also begins idle-timeout cleanup using configured `session_timeout`
5. **Skills service** (`skills.Service`) — scans Agent Skills roots, resolves precedence, exposes effective/shadowed/invalid records
6. **Built-in tools** — `terminal_execute`, `file_viewer`, `file_editor`, `render_component`, `activate_skill`
7. **History service** (`internal/history/`) — stores conversation history with sliding window
8. **File utility** (`internal/fileutil/`) — file path validation, workspace checks, read/write operations
9. **Runner manager** (`runner.NewManager`) — caches ADK runner, hot-reloads on config or skills-catalog changes
10. **HTTP server** (`api.NewServer`) — registers routes via `net/http` (Go 1.22+ ServeMux), delegates runner creation to RunnerManager; prints workspace + URL and optionally opens URL via `xdg-open`

Key lifecycle: sets up graceful shutdown via `signal.NotifyContext` → notifies active SSE clients, cancels active runs, closes executors, then shuts down HTTP.

### `internal/history/` — Conversation session manager

| File | Responsibility |
|------|---------------|
| `session.go` | `SessionManager` — per-chat LLM conversation history with sliding window cap |
| `session_test.go` | Unit tests for session lifecycle, history, sliding window |

`SessionManager` stores per-session message history with a configurable exchange cap. System prompt stored separately and prepended on reads. Session creation, user/assistant/tool message append, sliding-window trim, and close are supported operations. Lost on server restart.

### `internal/fileutil/` — File path validation and operations

| File | Responsibility |
|------|---------------|
| `path.go` | `ValidateWorkspacePath`, `ValidatePathWithAllowed` — workspace path validation |
| `path_test.go` | Unit tests for path validation |
| `filetools.go` | `ReadFile`, `ReadFileWithLineInfo`, `LineHash`, `EditFile`, `InsertLine`, `WriteFile`, `ListDirectory`, `FileViewerResult` |
| `filetools_test.go` | Unit tests for file operations |

Used by `file_viewer` and `file_editor` tools for all file I/O and path validation. Workspace-aware: all path operations validate against allowed directories.

### `internal/provider/` — provider profiles + caller-facing seams

| File | Responsibility |
|------|---------------|
| `discovery.go` | `DiscoverModels()` — caller-facing model-discovery seam. Resolves auth, refreshes provider-owned auth when needed, fetches selectable Models, and returns any refreshed auth state as data for caller persistence. |
| `chat.go` | `NewChatModel()` — caller-facing chat seam. Resolves auth, refreshes provider-owned auth when needed, and returns ready-to-use ADK `model.LLM` plus optional refreshed auth state for caller persistence. |
| `openai_model.go` | Provider-owned OpenAI-compatible `model.LLM` transport used by `NewChatModel()`. |
| `profiles.go` | Provider-internal profile table plus caller-safe metadata descriptors (`Describe`, `MustDescribe`). |
| `auth.go` | Provider-owned auth helpers, including config-auth validation/normalization, GitHub Copilot device-flow start/poll status mapping, token-to-auth-state conversion, and refresh. |

**Caller contract**: caller modules use narrow Provider seams, not raw profile/auth/transport internals. `config` reads caller-safe metadata via `Describe()` / `MustDescribe()` and validates persisted credentials via `ValidateCredentials()` plus `NormalizeConfigAuthState()`. Settings load/save, `/api/models`, and post-device-flow model refresh use `DiscoverModels()`. Chat-run startup uses `NewChatModel()`. GitHub device-flow UI polls through caller-safe `PollGitHubCopilotDeviceFlow()` status + `AuthUpdate`, not raw OAuth token payload handling. Provider package never writes app config itself; callers persist returned auth updates when needed.

Built-in tools: `terminal_execute`, `file_viewer`, `file_editor`, `render_component`, `activate_skill`. Implementations live in `internal/tool/`. The `activate_skill` tool delegates to `internal/skills` and returns structured skill instructions/resources for the current session.

### `internal/api/` — HTTP server + Templ templates

| File | Responsibility |
|------|---------------|
| `server.go` | `Server` struct — route registration, config CRUD, SSE handler, render endpoints |
| `templates/` | Templ source files (`.templ` → Go via `templ generate`) |
| `assets/` | Pinned frontend assets served from `embed.FS` (HTMX, Prism, KaTeX, Mermaid, and stylesheet assets). |

Route contract: `api.Server` registers routes via Go 1.22+ ServeMux. SSE packets are JSON-enveloped events with `event`, `data`, and optional `id` fields. Architecture note: Settings page load/save and `/api/models` cross one model-discovery seam: `provider.DiscoverModels()`, then persist any returned auth refresh through `persistAuth` callback or `config.Save`. GitHub Copilot device-flow UI still lives in API layer, but its poll step now consumes provider-owned status + `AuthUpdate` from `provider.PollGitHubCopilotDeviceFlow()` instead of decoding raw OAuth token payloads in handler code. `RunService.StartRun()` uses `provider.NewChatModel()` as its single chat seam and any auth refresh is persisted automatically via `PersistAuth` callback. `/api/sessions/{id}/stream` subscribes to active run state via `RunService.Subscribe()` after validating `browser_id` ownership and never starts runs. Active runs own their subscriber set, so multiple EventSource clients and reconnects are fan-out safe and disconnected clients cannot block run completion. Run start snapshots user-configured runtime limits such as `max_turns`, so later Settings changes affect only later runs; when a run hits that cap, server appends a friendly assistant message instead of hanging. Completion endpoints under `/api/sessions/{id}/complete/*` also validate `browser_id` ownership and return JSON for the composer island, not HTML fragments. The top-level HTTP handler also owns cross-cutting middleware: 1MB POST/PUT body limits and structured per-request logging (`method`, `path`, `status`, `duration_ms`, `session_id`).

**UI session state**: `api.Server` owns in-memory `UISession` records for browser-facing state: `id`, `browser_id`, `title`, `status` (`idle`/`running`/`error`), renderable messages/events, active run buffers, active skills, and timestamps. Server also exposes launch workspace path, provider setup state, and token-usage/context-window estimates to templates. Server-owned run buffers are canonical assistant transcripts; browser token buffers are display-only. ADK session service remains model conversation state; templates render from `UISession`, not ADK internals. Sessions are not persisted. Stale `/sessions/{id}` full-page loads after restart redirect to `/`; API calls for missing sessions return friendly 404 fragments/JSON.

**Templ templates** colocated at `internal/api/templates/`:

| Template | Purpose |
|----------|---------|
| `base.templ` | HTML document shell + embedded pinned assets + browser island scripts |
| `chat.templ` | `ChatView` — workspace indicator, setup banner for invalid provider config, message list, input, visible Stop button, completion menu container, SSE target for selected session |
| `session_tabs.templ` | `SessionTabs` — top-bar session strip with title, status dot, close button, and new-session button |
| `settings.templ` | `SettingsView` — config form, provider + model selectors, custom system prompt |
| `skills.templ` | `SkillsView` — detected Agent Skills table, refresh action, diagnostics |
| `components/active_skill_chips.templ` | Active skill chips for the current chat session |
| `components/chat_bubble.templ` | User/assistant message bubbles |
| `components/tool_card.templ` | Unified tool card (running/done status) |
| `components/file_edit_card.templ` | Post-write diff/created-file card for `file_editor` results; overwrite mode reuses shared interactive diff viewer |
| `components/error_toast.templ` | Error banner, auto-dismiss |
| `components/mermaid_diagram.templ` | Mermaid diagram container |
| `components/quick_replies.templ` | Suggestion chip buttons |
| `components/diff_card.templ` | Shared interactive diff viewer for DiffCard components and file edit overwrite results |

### `internal/skills/` — Agent Skills discovery + activation

Package owns Agent Skills scanning, parsing, precedence resolution, diagnostics, resource manifests, and activation. Skills are discovered from fixed project/user roots containing `SKILL.md`; precedence follows last-wins scoping.

| File | Responsibility |
|------|---------------|
| `skills.go` | Core types: `Skill`, `Scope`, `Status`, `Diagnostic`, `ActivatedSkill` |
| `discover.go` | Scan fixed skill roots for subdirectories containing `SKILL.md` |
| `parse.go` | Extract YAML frontmatter and Markdown body with lenient validation |
| `registry.go` | Resolve precedence, effective map, shadowed records, lookup by name |
| `resources.go` | Build capped resource manifests under `scripts/`, `references/`, and `assets/` |
| `skills_test.go` | Unit tests for roots, precedence, validation, diagnostics, activation caps |

**Service API**:
```go
type Service struct { ... }

func (s *Service) Refresh(ctx context.Context) (*Registry, error)
func (s *Service) Current() *Registry
func (s *Service) Activate(ctx context.Context, sessionID, name string) (*ActivatedSkill, error)
```

`api.Server` stores active skill names per UI session. The `activate_skill` tool (in `internal/tool/`) delegates to `skills.Service`. At chat-run start, `runner.RunService` re-resolves those active names against current effective registry state, drops disappeared/invalid/shadowed Skills with a warning, and injects ephemeral `activate_skill` tool-call context into that Run's LLM request so Skill instructions re-apply without permanently duplicating them into conversation history. API and runner packages consume this service; they never scan skill files directly.

### `internal/runner/` — Run service + runner cache

| File | Responsibility |
|------|---------------|
| `service.go` | `RunService` — run lifecycle: agent building, runner cache, SSE broadcast, session persistence, auth persist callbacks |
| `service_test.go` | Unit tests exercising the RunService seam (StartRun → Subscribe → AppendEvent) |
| `manager.go` | `Manager` struct — caches ADK `runner.Runner`, hot-reloads on config change |
| `manager_test.go` | Table-driven tests for cache hit, cache miss, invalidation |

**RunService**: consolidates run lifecycle behind a single seam. `StartRun()` builds the agent (LLM service → skill context → tool registry), gets or creates a cached runner, and starts the agent loop. Conversation history is managed via `internal/history.SessionManager`; file operations go through `internal/fileutil`. `Subscribe()`/`Unsubscribe()` manage SSE fan-out. `AppendEvent()` processes ADK events, broadcasts SSE events, and persists assistant messages. `Cancel()`/`CancelAll()` stop active runs. Auth refresh persistence is handled via a `PersistAuth` callback, not duplicated in API handlers.

**Runner caching**: `Manager.GetOrCreate()` creates ADK `runner.Runner` lazily and caches it by config hash (`provider|apiKey|baseURL|model|systemPrompt`). Changing settings in the UI triggers runner invalidation — new runner created on next message. `Invalidate()` forces rebuild on next call.

### `internal/executor/` — command execution

| File | Responsibility |
|------|---------------|
| `executor.go` | `CommandExecutor` interface — abstract command execution |
| `tmux.go` | Real tmux implementation |
| `session.go` | `SessionManager` — per-session executor lifecycle, idle timeout |
| `audit.go` | `RunAudit()` — preflight check for tmux binary |
| `mock.go` | `MockExecutor` — test double with canned responses |

**CommandExecutor interface**:
```go
type CommandResult struct {
    Stdout     string
    Stderr     string
    ExitCode   int
    TimedOut   bool
    DurationMs int64
    Truncated  bool
}

type CommandExecutor interface {
    ExecuteCommand(ctx context.Context, command string) (CommandResult, error)
    Close() error
}
```

`ExecuteCommand` is final-only in v1. It returns after command completion, timeout, cancellation, or executor failure. Non-zero shell exit is represented in `CommandResult.ExitCode`, not as a Go error. Timeout sets `TimedOut`; context cancellation stops active command promptly and returns a context error.

**Tmux executor architecture**:
1. Starts a long-running tmux session with a shell loop inside
2. Commands sent via `tmux send-keys` with literal string (no shell interpolation)
3. Output captured via `tmux capture-pane`, delimited by sentinel markers
4. Shell state (env, cwd) persists between commands within same session
5. Configurable timeout per command (default 60s). Concurrent commands in the same session are rejected with a clear error. Initial tmux working directory is the launch workspace; later shell `cd` persists inside that tmux session only.
6. Output capped at 128 KiB; excess output sets `CommandResult.Truncated`.
7. Process group killed on `Close()`.
8. **Session death recovery** — if the tmux session is killed externally, `ExecuteCommand` recreates it automatically on the next call.

**SessionManager**:
- Map of `sessionID → managedSession` (holds `CommandExecutor` + timeout state)
- `GetOrCreate(sessionID)` — returns existing executor or creates new one
- `StartTimeoutLoop()` — background goroutine checks idle timeout every 30s (default 30m)
- `Close(sessionID)` — cancels/tears down one executor and frees the slot
- `CloseAll()` on graceful shutdown
- No session metadata/history persistence; server restart loses sessions

### `internal/config/` — configuration

Config schema with defaults, masking, validation, and environment variable names are defined in `internal/config/manager.go`. Architecture note: `config.Manager` owns atomic JSON file writes, secure config permissions (`~/.eitri` `0700`, config/temp files `0600`), default loading without file creation, provider validation/model discovery on save, `context_window_tokens` fallback defaults (256k tokens for UI estimates when provider/model metadata lacks context length), and hot-reload on `PUT /api/config` / runner creation. Config reads provider defaults through caller-safe Provider descriptors rather than raw profile internals. Config also persists provider-owned auth state in `provider_auth` for providers that need richer auth than plain `api_key`; `GET /api/config` must never expose that raw state back to browser clients.

## Frontend architecture

Architecture name: **HTMX + Templ shell with browser islands**. Server owns canonical state and rendering; browser islands own only local ephemeral UI state.

**Stack**: Templ (`.templ` → Go), HTMX, small custom-element/browser-island scripts, embedded CSS, Prism.js, KaTeX, Mermaid.js. No npm, bundler, Tailwind, or SPA framework. Only code-generation step is `templ generate`.

**Ownership boundary**:
- Go server owns canonical state, sessions, routing, validation, security boundaries, agent runs, assistant transcripts, and HTML rendering.
- Templ renders pages, fragments, and rich UI components.
- HTMX handles forms, navigation, partial updates, OOB swaps, indicators, and transitions.
- DOM is base UI state.
- Browser islands own only ephemeral widget state: stream buffer, completion menu, copy toggles, rendered-library lifecycle, diff view mode.
- No island owns canonical app state or global store.

**Island lifecycle**:
- Initialize on full page load and `htmx:afterSwap`.
- Idempotent setup: no duplicate handlers, double renders, or timer leaks.
- Read configuration from server-rendered `data-*` attributes.
- Tolerate missing Prism/KaTeX/Mermaid.
- Use text nodes or server-rendered sanitized HTML for untrusted content; never `innerHTML` from user/LLM data.

**Key islands**:
- `eitri-stream`: opens `/api/sessions/{id}/stream` only after chat POST trigger; parses JSON envelopes; batches display-only tokens; handles run phases, no-dead-air, reconnect state, cancellation UI, render endpoint dispatch, and final Markdown render by `message_id`.
- `eitri-composer`: owns textarea keyboard behavior and `/` skill + `@` file completion menu state; calls JSON completion endpoints with debounce/sequence checks; preserves HTMX chat submit as authoritative transport.
- `eitri-code-block`, `eitri-mermaid`, `eitri-diff-card`: local widget behavior for copy/wrap/show-all, Mermaid rendering, and diff view toggles.

**Asset strategy**: `internal/api/assets/` contains pinned vendor assets served from `embed.FS` to avoid CDN availability, offline, and privacy failure modes. Exact vendor versions are intentionally deferred until implementation; do not use CDN or npm/bundler.

**Generative UI seam**: `render_component` emits structured data; server renders Templ components; islands add optional browser-native behavior without turning app into an SPA.

## Data flow (chat request)

```mermaid
sequenceDiagram
    participant Browser as Browser (HTMX)
    participant API as api.Server
    participant RunSvc as runner.RunService
    participant RunnerMgr as runner.Manager
    participant Skills as skills.Service
    participant Agent as agent/
    participant Executor as executor/

    Browser->>API: GET /api/sessions/{id}/complete/skills or /complete/files
    API-->>Browser: JSON completion candidates
    Browser->>API: POST /api/sessions/{id}/chat
    API->>API: Validate message, provider setup, parse slash skills, ensure no active run
    API->>Skills: Refresh + activate slash skills
    Skills-->>API: effective catalog + active skills
    API->>API: Re-resolve session active skills, warn/drop stale ones
    API->>RunSvc: StartRun(sessionID, message)
    RunSvc->>RunSvc: Build agent, get or create runner
    RunSvc->>Agent: Run(ctx, sessionID, message)
    API-->>Browser: User bubble HTML + HX-Trigger: eitri:connectRunStream
    Browser->>API: GET /api/sessions/{id}/stream (browser_id cookie)
    API->>API: Attach per-run SSE subscriber to active run fan-out set
    
    loop Agent turn
        Agent->>Agent: LLM generates response (SSE token deltas)
        RunSvc->>RunSvc: Append delta to server-owned run buffer
        RunSvc-->>Browser: SSE: token (delta)
        Browser->>Browser: Display-only buffer, flush on newline or 50-100ms
        Agent->>Agent: LLM generates tool call
        RunSvc-->>Browser: SSE: tool_call
        Browser->>Browser: Activity panel entry only; no tool_call card rendered
        alt activate_skill
            Agent->>Skills: Activate(sessionID, name)
            Skills-->>Agent: structured skill_content
        else terminal_execute
            Agent->>Executor: tool executes (tmux)
            Executor-->>Agent: result
        else file_editor
            Agent->>Agent: validate workspace path, capture old content, write file
        end
        RunSvc-->>Browser: SSE: tool_result
        Browser->>API: POST /api/sessions/{id}/render {kind: "tool_card"}
    end

    RunSvc-->>Browser: SSE: done (message_id)
    Browser->>API: POST /api/sessions/{id}/render {kind: "markdown", message_id}
    API->>API: Compute usage footer from provider usage and model context metadata or 256k fallback
    API-->>Browser: goldmark-rendered server-owned assistant message (via unified /render)
```

## Extension points

### Adding a new built-in tool

1. Define tool in `internal/tool/` implementing the `Tool` interface
2. Register with `tool.NewRegistry().Register(...)`
3. Tool receives `context.Context` with `tool.SessionIDKey` for session-scoped state

### Extending Agent Skills support

1. Keep discovery/parsing/precedence logic in `internal/skills`; API and agent packages should consume the service API rather than scanning files directly.
2. Add new skill roots only through a documented precedence change and ADR update.
3. Keep `allowed-tools` advisory until Eitri has a real approval/permission model.
4. Preserve resource access invariant: `file_viewer` can read workspace and skill directories; `file_editor` remains workspace-only.

### Adding a new API route

1. In `internal/api/server.go`, add `mux.HandleFunc(...)` in `NewServer()`
2. Access `configMgr`, `sessionMgr`, `sessionSvc` via `s` fields
3. Check `r.Header.Get("HX-Request")` to distinguish full page vs HTMX partial

### Adding a new generative UI component

1. Add component name to `render_component` tool enum in `internal/tool/render_component.go`
2. Create Templ template in `internal/api/templates/components/`
3. Wire server-side dispatch in `/api/sessions/{id}/render` handler with `kind: "component"`
4. Add browser island initialization only if component needs local browser-native behavior

### Adding a browser island

1. Server renders custom element/container via Templ.
2. Island script lives in Base asset bundle or a small module served by `internal/api/assets/`.
3. Island reads configuration from `data-*` attributes.
4. Island never owns canonical application state.
5. Island initialization is idempotent across full page loads and HTMX swaps.
6. If island renders untrusted content, it uses text nodes or server-rendered sanitized HTML, never `innerHTML` from LLM/user data.
7. Island keyboard behavior preserves existing composer contracts unless explicitly changed.
8. Browser E2E test covers island behavior.

### Supporting a non-OpenAI backend

1. Study `internal/provider/openai_model.go` — implements LLM adapter interface
2. Create a new adapter file (e.g. `anthropic_model.go`) in `internal/litellm/`
3. Register in the adapter factory based on provider config

## Target repository layout

```text
eitri/
├── cmd/eitri/                 # Entry point
├── internal/
│   ├── history/               # Conversation session manager
│   ├── fileutil/              # File path validation and I/O operations
│   ├── api/                   # HTTP/SSE server, assets, Templ templates
│   ├── config/                # Config loading, validation, atomic writes
│   ├── executor/              # tmux command executor + session manager
│   ├── provider/              # Provider profiles + auth seams
│   ├── runner/                # Run lifecycle + agent loop
│   ├── tool/                  # Built-in tools
│   └── skills/                # Agent Skills discovery, registry, activation
├── scripts/install.sh
├── docs/
│   ├── ARCHITECTURE.md
│   ├── TESTING.md
│   ├── ROADMAP.md
│   ├── adr/
│   └── agents/
├── CONTEXT.md
├── AGENTS.md
├── go.mod / go.sum
└── initial.md                 # Historical vision
```

Tests are colocated as `*_test.go`. Browser E2E tests live under `internal/api` behind the `browser` build tag. Templ-generated `*_templ.go` files are committed next to `.templ` sources.

## Testing patterns

Canonical test commands, fixtures, browser setup, and per-layer coverage live in [TESTING.md](TESTING.md). Architecture-specific test seams: `CommandExecutor` is mockable via `internal/executor/mock.go`; API tests use `httptest`; browser E2E uses chromedp against server-rendered HTMX DOM.

## Key ADRs

ADR index lives in [CONTEXT.md](../CONTEXT.md#architecture-decisions).

## Runtime configuration

Config file (`~/.eitri/config.json`), listen address (`--listen` flag, default `127.0.0.1:8080`), and environment variable overrides are defined in `internal/config/manager.go`.
