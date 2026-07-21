# Remove Google ADK, adopt litellm transport + custom agent loop

Replace Google ADK SDK with a lightweight, self-owned agent loop built on `github.com/voocel/litellm` for LLM transport, keeping Eitri's existing provider auth/discovery/profile layer as the outer shell.

**Status**: Accepted

## Context

Eitri initially adopted Google ADK Go SDK (`google.golang.org/adk/v2`) for agent orchestration, tool dispatch, session management, and runner lifecycle. Over time the ADK surface became a burden:

- ADK pins `google.golang.org/genai` and transitive Google-cloud dependencies (`cloud.google.com/go`, `google.golang.org/api`, `google.golang.org/grpc`) — roughly 20+ indirect modules with breaking-upgrade risk.
- Eitri uses none of ADK's multi-agent, streaming-response, or evaluation features — only the basic single-agent loop.
- ADK's model interface (`model.LLM` → `GenerateContent` returning `iter.Seq2`) is abstract; Eitri's transport layer (hand-rolled `OpenAIModel`) bridges it. This bridge duplicates error handling, retry logic, and SSE parsing that a dedicated library should own.
- ADK's internal session representation (`[]*genai.Content`) leaks into provider, runner, and API modules, making the genai dependency hard to isolate.

The Go-native `github.com/voocel/litellm` package provides `Provider` implementations for OpenAI, Anthropic, OpenRouter, Gemini, and a generic `compat` adapter — all producing a normalized `litellm.Response` / `litellm.Stream` with proper error classification, retries, streaming, and tool-call handling. Swapping to litellm eliminates the hand-rolled transport and reduces LLM plumbing to a config-to-provider mapping.

## Decision

1. **Remove all ADK dependencies**: `google.golang.org/adk/v2`, `google.golang.org/genai`, `google.golang.org/api`, and their transitive Google-cloud tree.
2. **Adopt `github.com/voocel/litellm`** as the sole LLM transport library. The Eitri layer wraps litellm Providers inside a unified adapter that satisfies a minimal, project-owned `LLMService` interface.
3. **Keep Eitri's provider auth/discovery/profile layer** (`internal/provider/auth.go`, `discovery.go`, `profiles.go`). These handle GitHub OAuth device flow, token refresh, structured `provider_auth` state, model discovery/filtering, and UI-facing provider metadata. Litellm replaces only the chat HTTP transport inside `internal/provider/openai_model.go`.
4. **Keep in-memory session storage** with a sliding window cap (last 50 exchanges), managed by a `session.Manager` API (`AppendUser`, `AppendAssistant`, `AppendTool`). Same loss-on-restart semantics as v1.
5. **Build a synchronous agent loop**: `LLM → parse tool_calls → execute → feed back → LLM`, running in a goroutine with concurrent SSE fan-out to the UI. No state machine, no multi-agent routing.
6. **Tool definitions** live in `internal/tool/` with explicit Go structs + `JSONSchema() litellm.Schema` methods. A generic `SchemaOf[T]` helper (maps struct fields + `jsonschema:` tags to JSON Schema) reduces boilerplate.
7. **Session management** lives in `internal/history/` with sliding window cap (last 50 exchanges), managed by a `SessionManager` API (`AppendUser`, `AppendAssistant`, `AppendTool`). Same loss-on-restart semantics as v1.
7. **No runner cache** for the LLM wrapper — litellm `Client` construction is cheap. Auth resolution (the costly path) is already cached by Eitri's existing `resolveAuthForRequest` expiry check.
8. **OpenCode Go model routing**: prefix-based rules (`qwen*`/`minimax*` → Anthropic `/v1/messages`, everything else → OpenAI `/v1/chat/completions`). Default to OpenAI-compatible for unknown prefixes.
9. **GitHub Copilot adapter**: use litellm's `provider/openai` with `Config.Headers` map for `Editor-Version` and `User-Agent`. Auth token is resolved by Eitri's auth layer before provider creation; refresh means creating a new provider (cheap).

## Considered Options

### Keep ADK and add litellm alongside

ADK already wraps the transport. Adding litellm as a second transport option would mean maintaining two bridges and still carrying all ADK transitive dependencies. The overhead of removing ADK is a one-time cost; the overhead of keeping it is ongoing.

### Build agent loop from scratch without any LLM library

The hand-rolled `OpenAIModel` already demonstrates the maintenance burden of raw HTTP transport (SSE parsing, retry, error classification, tool-call streaming). Litellm owns these correctly. Building a third transport would repeat the same bugs.

### Event-driven agent state machine

A state machine with explicit LLMToken/ToolCall/ToolResult events is more flexible for multi-agent or human-in-the-loop scenarios. Eitri v1 has none of those. The synchronous loop is simpler and can be wrapped in an event-driven shell later if needed.

## Consequences

### Positive

- **~20 fewer transitive Go modules** — no `google.golang.org/genai`, `google.golang.org/grpc`, `cloud.google.com/go/*`. Faster builds, fewer CVEs to track.
- **Clean type separation** — litellm's `Message`/`Block`/`Tool` types are project-owned abstractions, not ADK/genai types leaked through every module boundary.
- **Litellm owns transport correctness** — SSE edge cases, retry backoff, error classification, tool-call delta aggregation. Eitri owns only the loop and tool dispatch.
- **Provider extensibility** — adding a new provider (e.g., Anthropic native, Gemini) is a 3-line factory change instead of implementing a full `model.LLM` bridge.

### Risks

- **Agent loop bugs** — the synchronous loop replaces ADK's battle-tested runner. Edge cases (tool errors mid-loop, context cancellation, max-turns exceeded) must be handled explicitly.
- **Session format migration** — existing in-memory conversations use `[]*genai.Content`. Must migrate to `[]litellm.Message` on upgrades or reset on restart (acceptable for v1).
- **Litellm version stability** — `github.com/voocel/litellm` is younger than ADK. Monitor for API breaks; pin to a known-good version.

### Migration

1. Write litellm adapter wrappers in `internal/provider/` (replaces `openai_model.go`).
2. Build agent loop in `internal/runner/` or new `internal/agent/` package.
3. Replace ADK runner with new loop in `internal/runner/service.go`.
4. Remove ADK imports across all modules.
5. Delete ADK-specific code (`internal/agent/openai_model.go` bridge, ADK session wrappers).
