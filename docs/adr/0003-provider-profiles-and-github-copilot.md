# Provider profiles and GitHub Copilot

Eitri will support GitHub Copilot with provider profile instead of separate LLM implementation or Copilot SDK runtime. `github_copilot` discovers models from `GET {base_url}/models`, filters to picker-enabled `/chat/completions` models, and sends chat through existing OpenAI-style streaming transport at `POST {base_url}/chat/completions` with Copilot-specific headers. Settings support both manual bearer-token entry and GitHub OAuth device flow; returned OAuth token is stored as provider-owned auth state and resolved through provider auth before discovery and chat.

**Status**: Accepted

## Consequences

Provider behavior becomes data-driven by profiles: default base URL, API-key requirement, model-discovery path/parser, chat-completions path, extra headers, and provider-auth helpers. Settings load/save and `/api/models` should cross one caller-facing discovery seam that returns discovered Models plus any refreshed auth state for caller persistence. GitHub device flow remains Settings-triggered UX, but start/poll/auth-state encoding/auth resolution live with provider logic instead of Settings-only handler branches.
