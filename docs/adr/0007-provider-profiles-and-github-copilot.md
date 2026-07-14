# Provider profiles and GitHub Copilot

Eitri will support GitHub Copilot with provider profile instead of separate LLM implementation or Copilot SDK runtime. `github_copilot` discovers models from `GET {base_url}/models`, filters to picker-enabled `/chat/completions` models, and sends chat through existing OpenAI-style streaming transport at `POST {base_url}/chat/completions` with Copilot-specific headers. Settings support both manual bearer-token entry and GitHub OAuth device flow; returned OAuth token is stored as provider bearer credential.

**Status**: Accepted

## Consequences

Provider behavior becomes data-driven by profiles: default base URL, API-key requirement, model-discovery path/parser, chat-completions path, and extra headers. GitHub device flow now lives in Settings UX and uses `EITRI_GITHUB_CLIENT_ID` plus GitHub OAuth device/token endpoints. `/responses`, Anthropic `/v1/messages`, and WebSocket transports stay deferred until existing streaming chat-completions contract proves insufficient.
