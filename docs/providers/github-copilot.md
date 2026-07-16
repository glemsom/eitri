# GitHub Copilot Provider

Eitri supports GitHub Copilot through provider profile `github_copilot`. Settings support GitHub OAuth device flow and manual bearer-token entry. Chat still uses OpenAI-style streaming chat completions.

## Configure in Settings

### Preferred: Authenticate with GitHub

1. Open **Settings**.
2. Set **Provider** to **GitHub Copilot**.
3. Click **Authenticate with GitHub**.
4. Open shown verification URL and enter shown code.
5. Wait for Settings to refresh.
   - Eitri stores returned `gho_...` token in provider-owned auth state in config and keeps token field masked in UI.
   - If GitHub also returns refresh metadata, Eitri stores that state and auto-refreshes expired OAuth credentials during later model discovery and chat requests.
   - Same resolved auth is then used for `GET {base_url}/models` and later chat requests.
6. Select model from discovered list and save.

Eitri ships with built-in GitHub OAuth client ID for this device flow, so no extra environment setup is required for normal use. `EITRI_GITHUB_CLIENT_ID` remains optional as server-side override for development or custom deployments.

### Manual token entry

1. Open **Settings**.
2. Set **Provider** to **GitHub Copilot**.
3. Enter GitHub/Copilot bearer token in token field.
   - OAuth user tokens such as `gho_...` are expected.
   - Token must belong to account/org with Copilot entitlement.
4. Keep **Base URL** empty/default unless using enterprise or data-residency endpoint.
5. Save settings.
   - If validation fails, Settings stays in place and shows visible inline error feedback.
6. Eitri calls `GET {base_url}/models` with bearer auth and fills model selector with picker-enabled chat models.
7. Select model from discovered list and save.

Manual entry still writes same provider-owned auth state, so Settings auth, model discovery, and runtime chat all resolve credentials through one Copilot auth seam.

## Credential refresh behavior

When stored GitHub OAuth credential includes `refresh_token` and expiry metadata, Eitri refreshes it automatically before model discovery and before chat runs that need fresh auth. Refreshed credential is persisted back to config, so later requests use newest access token without manual re-login.

Caveats:

- Legacy saved Copilot credentials without refresh metadata cannot be auto-refreshed; re-run **Authenticate with GitHub** once to store fresh OAuth state.
- Manual bearer tokens that are not refreshable continue to work as plain bearer tokens; if they expire or are revoked, re-enter or re-authenticate.

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
User-Agent: GithubCopilot/1.100.0
Editor-Version: vscode/1.80.0
X-GitHub-Api-Version: 2026-06-01
x-initiator: user
```

Requests use `stream: true`. Eitri expects OpenAI-style SSE text deltas and streaming tool-call deltas. Providers/models that cannot satisfy required streaming tool-call contract show friendly unsupported-provider error.

## Current limitations

Still deferred:

- OpenAI `/responses` endpoint.
- Anthropic `/v1/messages` endpoint.
- WebSocket transports, including `ws:/responses`.
- Vision-specific headers such as `Copilot-Vision-Request`.
- Broader long-lived Copilot credential strategies beyond stored OAuth refresh state and plain bearer tokens.

Current implementation focuses on normal text chat and tool-call streaming through `/chat/completions`.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `GitHub Copilot token required` | Missing token | Enter token or use **Authenticate with GitHub** in Settings |
| `GitHub device flow expired` | User took too long or code reused | Start device flow again |
| `GitHub Copilot OAuth not configured` | Server override/client ID misconfiguration | Restart Eitri or check server configuration |
| `Provider authentication failed` | Token invalid, expired, lacks Copilot entitlement, or org policy blocks Copilot | Use valid Copilot-enabled token; if stored OAuth refresh state is missing/stale, re-run **Authenticate with GitHub** |
| `github_copilot token expired and cannot refresh; re-authenticate` | Stored OAuth access expired but no usable refresh token is stored | Re-run **Authenticate with GitHub** |
| `github_copilot refresh token expired; re-authenticate` | Stored OAuth refresh token expired | Re-run **Authenticate with GitHub** |
| No models shown | Discovery failed or returned no picker-enabled chat models | Check token, base URL, enterprise endpoint, org policy |
| Unsupported-provider error during chat | Selected model/endpoint lacks required streaming/tool-call behavior | Select different discovered chat model |
| Cannot reach provider | Wrong base URL or network issue | Restore default URL or check enterprise endpoint |

## Automated testing expectation

Automated tests must use fake provider servers for Copilot discovery/chat behavior. Test suite must not depend on live GitHub or real Copilot entitlement.
