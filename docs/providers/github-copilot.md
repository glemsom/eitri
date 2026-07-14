# GitHub Copilot Provider

Eitri supports GitHub Copilot through provider profile `github_copilot`. MVP uses manual bearer-token entry and OpenAI-style streaming chat completions.

## Configure in Settings

1. Open **Settings**.
2. Set **Provider** to **GitHub Copilot**.
3. Enter GitHub/Copilot bearer token in token field.
   - OAuth user tokens such as `gho_...` are expected for MVP.
   - Token must belong to account/org with Copilot entitlement.
4. Keep **Base URL** empty/default unless using enterprise or data-residency endpoint.
5. Save settings.
   - If validation fails, Settings stays in place and shows visible inline error feedback.
6. Eitri calls `GET {base_url}/models` with bearer auth and fills model selector with picker-enabled chat models.
7. Select model from discovered list and save.

Eitri sends token as:

```text
Authorization: Bearer <token>
```

Empty token field preserves existing stored token. Use **clear token/API key** control when replacing config by removing stored secret.

## Base URLs

Default Copilot API base URL:

```text
https://api.githubcopilot.com
```

Enterprise or data-residency deployments can use custom Copilot API base URL, for example:

```text
https://copilot-api.<enterprise-domain>
```

Eitri appends provider-profile paths. Do not include `/v1` for Copilot.

| Operation | Path |
| --- | --- |
| Model discovery | `GET {base_url}/models` |
| Chat | `POST {base_url}/chat/completions` |

## Model discovery rules

Copilot model discovery reads `data[]` from `/models`. MVP model picker includes models that are:

- not disabled by policy,
- `model_picker_enabled == true`,
- advertised as supporting `/chat/completions`.

Eitri stores selected model ID. If provider changes, model selection is cleared and user must rediscover/select model again.

## Chat transport

Copilot chat uses same Eitri OpenAI-style streaming transport as other chat-completions providers, with Copilot-specific path and headers:

```text
POST {base_url}/chat/completions
Authorization: Bearer <token>
Accept: application/json
Content-Type: application/json
User-Agent: eitri
X-GitHub-Api-Version: 2026-06-01
Openai-Intent: conversation-edits
x-initiator: user
```

Requests use `stream: true`. Eitri expects OpenAI-style SSE text deltas and streaming tool-call deltas. Providers/models that cannot satisfy required streaming tool-call contract show friendly unsupported-provider error.

## MVP limitations

Deferred from MVP:

- OAuth device-flow login inside Eitri. Users manually paste bearer token.
- OpenAI `/responses` endpoint.
- Anthropic `/v1/messages` endpoint.
- WebSocket transports, including `ws:/responses`.
- Vision-specific headers such as `Copilot-Vision-Request`.

MVP focuses on normal text chat and tool-call streaming through `/chat/completions`.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `GitHub Copilot token required` | Missing token | Enter token in Settings |
| `Provider authentication failed` | Token invalid, expired, lacks Copilot entitlement, or org policy blocks Copilot | Use valid Copilot-enabled token |
| No models shown | Discovery failed or returned no picker-enabled chat models | Check token, base URL, enterprise endpoint, org policy |
| Unsupported-provider error during chat | Selected model/endpoint lacks required streaming/tool-call behavior | Select different discovered chat model |
| Cannot reach provider | Wrong base URL or network issue | Restore default URL or check enterprise endpoint |

## Automated testing expectation

Automated tests must use fake provider servers for Copilot discovery/chat behavior. Test suite must not depend on live GitHub or real Copilot entitlement.
