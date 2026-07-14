# Research: GitHub Copilot as an Eitri model provider

Date: 2026-07-14

## Summary

GitHub Copilot can be integrated as an HTTP model provider without using the Copilot SDK runtime. Auth can use a GitHub OAuth user token obtained with GitHub device flow, then sent directly as `Authorization: Bearer ...` to `https://api.githubcopilot.com`. Model discovery works through `GET /models`. Chat works through OpenAI-compatible-ish `POST /chat/completions`; newer GPT-5-class models can use `POST /responses`; Anthropic models may also advertise `/v1/messages`.

Tested locally with `gho_...` GitHub OAuth token from `gh auth token`:

- `GET https://api.githubcopilot.com/models` returned `200` and 35 models.
- `POST https://api.githubcopilot.com/chat/completions` with `gpt-4.1` returned `200` and normal OpenAI-style `choices` + `usage`.
- `POST https://api.githubcopilot.com/responses` with `gpt-5-mini` returned `200`, but tiny `max_output_tokens` produced `status: "incomplete"` and reasoning-only output. Endpoint exists; request shape accepted.
- `GET https://api.github.com/copilot_internal/v2/token` with same token returned `404`. Do not rely on this internal token-exchange endpoint for Eitri.

## Sources

- GitHub Copilot SDK auth docs: <https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/authenticate>
- GitHub OAuth device flow docs: <https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow>
- GitHub Copilot supported models docs: <https://docs.github.com/en/copilot/reference/ai-models/supported-models>
- GitHub Copilot model pricing docs: <https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing>
- OpenCode Copilot auth/model code:
  - <https://github.com/sst/opencode/blob/dev/packages/opencode/src/plugin/github-copilot/copilot.ts>
  - <https://github.com/sst/opencode/blob/dev/packages/opencode/src/plugin/github-copilot/models.ts>
  - <https://github.com/sst/opencode/blob/dev/packages/llm/src/providers/github-copilot.ts>
- Pi agent local source review (`~/Documents/git/private/pi`):
  - `packages/ai/src/utils/oauth/github-copilot.ts`
  - `packages/ai/src/utils/oauth/device-code.ts`
  - `packages/ai/src/providers/github-copilot.ts`
  - `packages/coding-agent/src/core/auth-storage.ts`
  - `packages/coding-agent/src/modes/interactive/components/login-dialog.ts`

## Authentication

### Recommended auth flow for Eitri

Use GitHub OAuth device flow for CLI/local agent UX.

GitHub docs say device flow is intended for headless apps/CLI tools. Flow:

1. App requests device/user codes from `POST https://github.com/login/device/code`.
2. App asks user to enter code at `https://github.com/login/device`.
3. App polls `POST https://github.com/login/oauth/access_token` until user approves, code expires, or server asks client to slow down.
4. App receives OAuth access token, usually `gho_...`.
5. Store token securely in OS keychain / Eitri credential store.
6. Use token as Copilot bearer token against `https://api.githubcopilot.com`.

Official Copilot SDK docs list supported token types for OAuth GitHub App mode:

- `gho_` OAuth user access token
- `ghu_` GitHub App user access token
- `github_pat_` fine-grained PAT

They explicitly say classic `ghp_` PAT is not supported.

OpenCode uses device flow with scope `read:user`, stores returned access token as both `access` and `refresh`, with `expires: 0`. No refresh token observed in GitHub device flow response.

Pi findings:

- Pi hard-codes a GitHub Copilot OAuth client ID in source instead of requiring user env configuration.
- Pi wraps Copilot auth behind a provider-owned OAuth interface, not Settings-owned special cases.
- Pi has generic device-code polling helper behavior worth copying: wait before first poll, honor `slow_down`, handle timeout/cancel cleanly.
- Pi auto-refreshes expired OAuth credentials under a storage lock before model requests.
- Pi also derives account-specific base URL and filters available model IDs from credential state.

Caution: Pi refresh path calls `https://api.<domain>/copilot_internal/v2/token`. Local Eitri testing saw `404` from `https://api.github.com/copilot_internal/v2/token`, so Eitri should not assume this internal exchange endpoint is reliable just because Pi uses it.

### Device flow cURL

Create own GitHub OAuth App for Eitri. Enable device flow in app settings.

Implementation note from Pi review: shipping a built-in Eitri client ID gives better UX than requiring users to set one manually. If Eitri owns this OAuth app, embed its client ID in code and treat it as product configuration, not user configuration.

```bash
export GITHUB_CLIENT_ID="<eitri-oauth-app-client-id>"

curl -sS -X POST https://github.com/login/device/code \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: eitri' \
  -d "$(jq -nc --arg client_id "$GITHUB_CLIENT_ID" '{client_id:$client_id, scope:"read:user"}')"
```

Response shape:

```json
{
  "device_code": "...",
  "user_code": "WDJB-MJHT",
  "verification_uri": "https://github.com/login/device",
  "expires_in": 900,
  "interval": 5
}
```

Show user:

```text
Open https://github.com/login/device and enter WDJB-MJHT
```

Poll, respecting `interval`. If response error is `authorization_pending`, keep waiting. If `slow_down`, add at least 5 seconds or use returned `interval`. GitHub docs call out both errors.

```bash
export DEVICE_CODE="<device_code from previous response>"

curl -sS -X POST https://github.com/login/oauth/access_token \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: eitri' \
  -d "$(jq -nc \
    --arg client_id "$GITHUB_CLIENT_ID" \
    --arg device_code "$DEVICE_CODE" \
    '{client_id:$client_id, device_code:$device_code, grant_type:"urn:ietf:params:oauth:grant-type:device_code"}')"
```

Success response:

```json
{
  "access_token": "gho_...",
  "token_type": "bearer",
  "scope": "read:user"
}
```

Store `access_token`. Use as `COPILOT_TOKEN` in examples below.

### GitHub Enterprise / data residency

OpenCode maps GitHub Enterprise domain to:

- OAuth device endpoint: `https://<enterprise-domain>/login/device/code`
- OAuth token endpoint: `https://<enterprise-domain>/login/oauth/access_token`
- Copilot API base: `https://copilot-api.<enterprise-domain>`

For github.com, Copilot API base is:

```text
https://api.githubcopilot.com
```

## Model discovery

Use:

```bash
export COPILOT_TOKEN="gho_..."

curl -sS https://api.githubcopilot.com/models \
  -H "Authorization: Bearer $COPILOT_TOKEN" \
  -H 'Accept: application/json' \
  -H 'User-Agent: eitri' \
  -H 'X-GitHub-Api-Version: 2026-06-01' \
  | jq '.data[] | {id, name, version, model_picker_enabled, supported_endpoints}'
```

Observed response top-level shape:

```json
{
  "data": [
    {
      "id": "claude-opus-4.6",
      "name": "Claude Opus 4.6",
      "version": "claude-opus-4.6",
      "model_picker_enabled": true,
      "supported_endpoints": ["/v1/messages", "/chat/completions"],
      "capabilities": { "family": "claude-opus-4.6", "limits": {}, "supports": {} },
      "billing": { "token_prices": {} },
      "policy": { "state": "enabled" }
    }
  ]
}
```

Observed model item keys:

```text
billing, capabilities, id, is_chat_default, is_chat_fallback,
model_picker_category, model_picker_enabled, model_picker_price_category,
name, object, policy, preview, supported_endpoints, vendor, version
```

Eitri should filter models:

- `policy.state != "disabled"`
- `model_picker_enabled == true` if showing user-selectable models
- `supported_endpoints` contains endpoint Eitri can call
- `capabilities.limits.max_context_window_tokens` or `max_prompt_tokens` for context limit
- `capabilities.limits.max_output_tokens` for max output
- `capabilities.supports.tool_calls` for tool support
- `capabilities.supports.vision` or `limits.vision.supported_media_types` for image support
- `billing.token_prices` for cost display if needed

OpenCode uses exactly this style: fetch `/models`, parse custom Copilot fields, map endpoint from `supported_endpoints`.

## Chat completions endpoint

Endpoint:

```text
POST https://api.githubcopilot.com/chat/completions
```

This endpoint is OpenAI Chat Completions compatible enough for normal message calls.

Test cURL:

```bash
export COPILOT_TOKEN="gho_..."

curl -sS https://api.githubcopilot.com/chat/completions \
  -H "Authorization: Bearer $COPILOT_TOKEN" \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: eitri' \
  -H 'X-GitHub-Api-Version: 2026-06-01' \
  -H 'Openai-Intent: conversation-edits' \
  -H 'x-initiator: user' \
  -d '{
    "model": "gpt-4.1",
    "messages": [
      {"role": "user", "content": "Reply with exactly: pong"}
    ],
    "temperature": 0,
    "max_tokens": 10,
    "stream": false
  }' | jq
```

Observed success response:

```json
{
  "id": "chatcmpl-...",
  "model": "gpt-4.1-2025-04-14",
  "choices": [
    {
      "index": 0,
      "message": {
        "content": "pong",
        "padding": "abcdefghi",
        "role": "assistant"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "completion_tokens": 2,
    "prompt_tokens": 12,
    "total_tokens": 14
  }
}
```

Notes:

- Response included non-standard `message.padding`; parser must ignore unknown fields.
- OpenCode omits `maxOutputTokens` for GPT models to match Copilot CLI behavior. Eitri can start with `max_tokens` but should tolerate model-specific rejection.
- For vision requests, OpenCode adds `Copilot-Vision-Request: true` when any image input exists.
- OpenCode sets `x-initiator: user` or `x-initiator: agent`. It uses `agent` for subagents, compaction, and tool/continuation-like flows.

## Responses endpoint

Endpoint:

```text
POST https://api.githubcopilot.com/responses
```

Use when `/models` says `supported_endpoints` contains `/responses` or `ws:/responses`. OpenCode routes GPT-5-class models to Responses by default, except `gpt-5-mini` variants remain chat unless explicitly configured.

Test cURL:

```bash
export COPILOT_TOKEN="gho_..."

curl -sS https://api.githubcopilot.com/responses \
  -H "Authorization: Bearer $COPILOT_TOKEN" \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: eitri' \
  -H 'X-GitHub-Api-Version: 2026-06-01' \
  -H 'Openai-Intent: conversation-edits' \
  -H 'x-initiator: user' \
  -d '{
    "model": "gpt-5-mini",
    "input": [
      {
        "role": "user",
        "content": [{"type": "input_text", "text": "Reply with exactly: pong"}]
      }
    ],
    "max_output_tokens": 64,
    "stream": false
  }' | jq
```

Observed with `max_output_tokens: 16`: request accepted with `200`, response had `status: "incomplete"` and reasoning output only. Conclusion: endpoint exists and accepts OpenAI Responses-like request shape; use adequate output token budget and test per model.

## Anthropic Messages endpoint

Some Copilot models advertise:

```json
"supported_endpoints": ["/v1/messages", "/chat/completions"]
```

OpenCode maps these to Anthropic SDK shape at base URL:

```text
https://api.githubcopilot.com/v1
```

So endpoint becomes:

```text
POST https://api.githubcopilot.com/v1/messages
```

Use only when needed. Eitri can likely start with `/chat/completions` for all models that advertise it, then add `/v1/messages` if Anthropic-specific features are needed.

## Header checklist

Minimum headers used in tests:

```text
Authorization: Bearer <GitHub OAuth token>
Accept: application/json
Content-Type: application/json
User-Agent: eitri
X-GitHub-Api-Version: 2026-06-01
```

Headers copied from OpenCode behavior for chat calls:

```text
Openai-Intent: conversation-edits
x-initiator: user
```

Conditional headers:

```text
Copilot-Vision-Request: true              # if request includes image input
X-Interaction-Type: agent-session-name-generation  # title/name generation
anthropic-beta: interleaved-thinking-2025-05-14    # Anthropic messages, if needed
```

## Error handling

Implement these cases:

- `401` / `403`: token invalid, token expired/revoked, no Copilot entitlement, org policy blocks use.
- `404` from `api.github.com/copilot_internal/v2/token`: expected in local test; avoid this endpoint.
- `405` on `GET/POST` mismatch: `/chat/completions` requires `POST`.
- OAuth device flow `authorization_pending`: keep polling.
- OAuth device flow `slow_down`: increase polling interval by 5 seconds or honor returned interval.
- OAuth device flow `expired_token` / `token_expired`: restart device flow.

## Implementation recommendation for Eitri

1. Add provider-owned auth seam for `github_copilot` so login, stored credential shape, request auth resolution, and future refresh behavior live with provider logic instead of Settings-only branches.
2. Ship built-in GitHub Copilot OAuth client ID in Eitri and use scope `read:user` for device flow; do not require user env configuration for client ID.
3. Copy Pi-style device-code polling semantics: wait before first poll, honor server `slow_down` intervals, and support cancellation/timeouts cleanly.
4. Store returned credential in Eitri config/credential store as provider-owned auth state.
5. Discover models with `GET {base_url}/models` using bearer token.
6. Build model list from `data[]`, filtering disabled/non-picker models for user selection.
7. For each model, choose endpoint:
   - prefer `/responses` for GPT-5+ models when advertised and Eitri supports Responses parsing
   - else use `/chat/completions` when advertised
   - defer `/v1/messages` until Anthropic-specific support needed
8. Send chat with OpenAI-compatible request/response parser that ignores unknown fields.
9. Add provider-specific headers above.
10. Add automatic re-auth/refresh path for expired Copilot credentials, but prefer public-token strategy first. Pi's internal `copilot_internal/v2/token` exchange may inform design, yet Eitri should not depend on that endpoint unless separately validated in Eitri environments.

## Open questions

- Whether `github_pat_...` fine-grained PAT works directly against `api.githubcopilot.com`; official SDK auth docs say supported by SDK, but local direct-endpoint test used `gho_...` only.
- Whether public GitHub OAuth device-flow token remains sufficient long-term for all Copilot API calls, or whether some tenants require Pi-style extra token exchange.
- Whether Pi's `copilot_internal/v2/token` refresh path works only for certain domains/base URLs (`api.individual.githubcopilot.com`, enterprise hosts) while failing against `api.github.com` in local Eitri testing.
- Exact minimal header set for every model family. Local test included OpenCode-style headers; `/models` worked with bearer + version + user agent.
- Streaming SSE behavior for `/chat/completions` and `/responses`; not tested yet.
- WebSocket `ws:/responses` support; advertised by `/models`, not tested.
