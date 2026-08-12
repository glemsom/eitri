# ADR 0004 — Provider breadth: one seam, three families, TUI-only Copilot auth

- Status: accepted
- Date: 2026-08-12
- Related: ticket [Provider breadth: GitHub Copilot + custom OpenAI (#37)](https://github.com/glemsom/eitri/issues/37); builds on [Robust tool-call dispatch (#30)](https://github.com/glemsom/eitri/issues/30) (T5) and the TUI shell [TUI shell (#34)](https://github.com/glemsom/eitri/issues/34) (T9a).

## Context

Eitri must be provider-agnostic over the three documented dialect families
(eitri.md §2.2): the primary **OpenCode Go** provider, **GitHub Copilot**
(device-flow OAuth), and any **custom OpenAI-compatible** endpoint. The two
requirements that drive every decision in this ticket:

1. **One canonical schema per tool, re-expressed per dialect.** T5 introduced
   `provider.ReExpress` — a single serializer from `[]DialectDefinition` into a
   wire-dialect tool manifest. No provider may author per-dialect copies; every
   family routes through that one serializer.
2. **The interactive Copilot device-flow handshake is TUI-only.** Batch never
   shows a device flow. It consumes stored/refreshed credentials non-interactively;
   when neither exists it fails cleanly asking the user to re-auth in the TUI.

## Decision

1. **A provider factory dispatches on the saved `config.Provider` value, honored
   across TUI and batch.** `provider.FromConfig(cfg, env)` builds One of
   `OpenAICompatible` (opencode-go / custom-openai) or `CopilotProvider`
   (github-copilot). Unsupported selections and a custom-openai pick with no
   endpoint are explicit errors. The run engine and both run kinds select their
   provider through this factory — no per-kind provider construction.

2. **Copilot is the same Chat-Completions wire, only authentication differs.**
   `CopilotProvider` reuses the shared `chatCompletionBody` + `openAIStream` SSE
   parse (content, `reasoning_content`, `tool_calls`, streamed `usage`) — so
   Copilot reasoning/streaming lands in the exact reasoning channel the engine
   already handles. It differs only in how the bearer token is resolved.

3. **Batch token resolution is: stored-valid → refresh → fail.** `CopilotProvider`
   uses a stored access token when present (and not past `expires_at`); else a
   refresh token, when present, renews non-interactively via the OAuth token
   endpoint (automatic credential renewal, allowed for batch) and persists the new
   tokens to config; else it returns `ErrReauthRequired` — a clean error directing
   the user to re-auth in the TUI. Batch never invokes the device flow.

4. **The TUI owns device-flow OAuth.** `provider.DeviceFlow` (start → show
   verification URI/user code in-UI → poll → fresh token) is the TUI-side
   handshake. `app.CopilotConnect` runs the full handshake and persists the
   fresh token set to `config.Copilot`, so the interactive screen can call one
   function end-to-end and later batch runs reuse the token.

5. **Copilot + custom-OpenAI credentials live in config** (`config.Copilot`,
   `config.CustomOpenAI`), unlike the primary provider's env-delivered key — they
   are user-configured and must survive across runs.

## Consequences

- One seam (`provider.Provider` + `ReExpress`) covers all three families; the
  engine and test fixtures are unchanged across providers.
- Batch Copilot behavior is deterministic and testable at the provider seam
  (`refresh`, `no-refresh → reauth`, `works-after-reauth`).
- OpenCode Go keeps the env-delivered `OPENCODE_API_KEY`; adding a real Copilot
  chat round-trip still needs the endpoint's exact request shape verified at
  integration time (the device-flow client and refresh/chat HTTP are wired and
  unit-tested against stubs).
- The device-flow authentication → token persistence → batch reuse path is
  complete and callable via `app.CopilotConnect`.

## Remaining interactive UI (out of ADR scope)

Binding a Bubble Tea **approval screen + keybinding** to `app.CopilotConnect` is
interactive TUI rendering work separate from the seam decisions above; it has no
normative behavior for batch (batch never runs the device flow). Until that
screen lands, Copilot authentication is reachable through `app.CopilotConnect`
in headless/unattended wiring and the TUI settings surface allows selecting the
provider.
