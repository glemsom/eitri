# GitHub Copilot Provider — issue drafts

`gh issue create` failed for current GitHub account (`createIssue` unauthorized). Copy these into GitHub issues when permissions are available. Apply label: `ready-for-agent`.

---

## 1. Provider profiles preserve existing OpenAI-compatible providers end-to-end

Label: `ready-for-agent`

### What to build

Introduce provider profiles as the single path for existing OpenCode Go and custom OpenAI-compatible providers, without changing user-visible behavior. OpenCode Go and custom OpenAI should still save config, discover models, and start streaming chats through their existing OpenAI-compatible endpoints.

### Acceptance criteria

- [ ] Existing `opencode_go` and `custom_openai` config save/model discovery flows still work through provider profiles.
- [ ] Existing chat runs still use `/v1/chat/completions` with streaming text and tool calls.
- [ ] Existing model discovery still uses `/v1/models` and parses OpenAI-style `data[].id`.
- [ ] Provider validation/API-key requirement behavior remains unchanged for OpenCode Go and custom OpenAI.
- [ ] Tests cover both existing providers through the new profile path.

### Blocked by

None - can start immediately

---

## 2. GitHub Copilot config discovers picker-enabled chat models

Label: `ready-for-agent`

### What to build

Add GitHub Copilot as a selectable Provider in Settings. A user can enter a GitHub/Copilot bearer token and Copilot base URL, save config, and populate the model selector from Copilot's picker-enabled chat models.

### Acceptance criteria

- [ ] Settings offers `github_copilot` with display name `GitHub Copilot`.
- [ ] GitHub Copilot requires a bearer credential stored in `api_key` and surfaces a clear missing-token error.
- [ ] GitHub Copilot default base URL is `https://api.githubcopilot.com`.
- [ ] Model discovery calls `GET {base_url}/models` with Copilot headers and bearer auth.
- [ ] Model discovery includes only models with `policy.state != "disabled"`, `model_picker_enabled == true`, and `/chat/completions` in `supported_endpoints`.
- [ ] Settings and `/api/models` return discovered Copilot model IDs for selection.
- [ ] Tests cover successful discovery, filtering, auth failure, and missing token.

### Blocked by

- Issue 1: Provider profiles preserve existing OpenAI-compatible providers end-to-end

---

## 3. GitHub Copilot streams chat through OpenAI-style transport

Label: `ready-for-agent`

### What to build

Make GitHub Copilot usable for normal Eitri chat runs by sending streaming chat-completions requests through the existing OpenAI-style transport, using Copilot-specific endpoint paths and headers.

### Acceptance criteria

- [ ] GitHub Copilot chat requests call `POST {base_url}/chat/completions`, not `/v1/chat/completions`.
- [ ] Copilot chat requests include required bearer auth plus `User-Agent`, `X-GitHub-Api-Version`, `Openai-Intent`, and `x-initiator` headers.
- [ ] Copilot chat uses `stream: true` and parses OpenAI-style SSE text deltas.
- [ ] Copilot chat parses streaming tool-call deltas through the same tool-call assembly path as existing providers.
- [ ] Unsupported/malformed streaming behavior produces the existing friendly unsupported-provider error.
- [ ] Fake-provider integration tests cover Copilot streaming text and tool calls without live GitHub dependency.

### Blocked by

- Issue 2: GitHub Copilot config discovers picker-enabled chat models

---

## 4. Provider switching keeps config safe and obvious

Label: `ready-for-agent`

### What to build

Make provider switching safe in Settings so users do not accidentally keep stale model IDs or overwrite custom base URLs. Switching provider should guide users back through model discovery without losing intentional custom URL/token choices.

### Acceptance criteria

- [ ] Changing `provider` clears the saved `model`.
- [ ] Changing `provider` resets `base_url` to the new provider default only when the previous base URL was empty or equal to the old provider default.
- [ ] Changing `provider` preserves custom base URLs.
- [ ] Empty API key/token input still preserves the existing stored secret unless `clear_api_key` is selected.
- [ ] Settings labels/hints make clear that OpenCode Go needs an API key, GitHub Copilot needs a token, and custom OpenAI credentials are optional.
- [ ] Tests cover JSON save and HTMX form save paths for provider switching.

### Blocked by

- Issue 1: Provider profiles preserve existing OpenAI-compatible providers end-to-end

---

## 5. Document GitHub Copilot provider operation

Label: `ready-for-agent`

### What to build

Document how users and agents should configure and operate the GitHub Copilot Provider, including token expectations, base URL defaults, model selection, enterprise base URLs, and MVP limitations.

### Acceptance criteria

- [ ] User-facing docs explain selecting GitHub Copilot, entering a bearer token, saving to discover models, then selecting a model.
- [ ] Docs mention default Copilot base URL `https://api.githubcopilot.com` and custom enterprise/data-residency base URL support.
- [ ] Docs state OAuth device flow is deferred and manual token entry is the MVP.
- [ ] Docs state `/responses`, Anthropic `/v1/messages`, WebSocket transports, and vision-specific headers are deferred.
- [ ] Testing/runbook docs mention fake-provider testing and no live GitHub dependency for automated tests.

### Blocked by

- Issue 2: GitHub Copilot config discovers picker-enabled chat models
- Issue 3: GitHub Copilot streams chat through OpenAI-style transport
