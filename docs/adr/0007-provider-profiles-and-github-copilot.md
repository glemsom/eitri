# Provider profiles and GitHub Copilot

Eitri will support GitHub Copilot with a provider profile instead of a separate LLM implementation or Copilot SDK runtime. The `github_copilot` profile uses a manually supplied bearer token for MVP, discovers models from `GET {base_url}/models`, filters to picker-enabled `/chat/completions` models, and sends chat through the existing OpenAI-style streaming transport at `POST {base_url}/chat/completions` with Copilot-specific headers.

**Status**: Accepted

## Consequences

Provider behavior becomes data-driven by profiles: default base URL, API-key requirement, model-discovery path/parser, chat-completions path, and extra headers. OAuth device flow, `/responses`, Anthropic `/v1/messages`, and WebSocket transports are deferred until the existing streaming chat-completions contract proves insufficient.
