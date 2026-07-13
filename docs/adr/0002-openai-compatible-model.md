# OpenAI-compatible model abstraction

Implement `model.LLM` directly via HTTP calls to `/v1/chat/completions` instead of using provider-specific SDKs. One implementation serves any OpenAI-compatible backend — OpenAI, OpenCode Go, Ollama, vLLM — by changing `base_url` in config. Provider-specific quirks (error formats, streaming behavior) become our maintenance responsibility.

**Status**: Accepted

## Consequences

Single HTTP client vs. multiple provider SDKs: simpler dependency graph, but we absorb provider-specific differences ourselves. No native support for non-OpenAI-protocol backends (e.g., Anthropic Messages API) — those would need separate `model.LLM` implementations. Token counting relies on server-reported `usage` fields or client-side estimation, which is less precise than provider-native SDKs.
