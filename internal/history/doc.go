// Package history manages per-session LLM conversation history with a sliding
// window cap on exchanges. A session belongs to one chat conversation;
// each exchange begins with a user message and includes all assistant and
// tool messages that follow. The system prompt is stored separately and
// prepended on reads.
//
// Deprecated: this store is being contracted away (umbrella #1231). Since
// issue #1241 the loop's session-backed history adapter reads and writes the
// canonical session store (`internal/session`) directly, so no production
// code path uses SessionManager anymore — it survives only to hold the parity
// tests that prove the canonical store behaves identically
// (`internal/session/exchange_parity_test.go`,
// `internal/runner/loop/adapters_test.go`) and will be deleted by issue
// #1242.
//
// SessionManager is the central type — it creates named sessions, appends
// messages, retrieves full history (system prompt + window), and evicts
// oldest exchanges when the window cap is exceeded. State is in-memory only
// and lost on server restart.
//
// Key types:
//   - SessionManager — manages named conversation sessions
//
// Key interfaces: none (concrete type, no interface contract)
//
// Key constants:
//   - DefaultMaxExchanges — default sliding window cap (150)
//   - DefaultSystemPrompt — fallback system prompt (aliases persona.DefaultPrompt)
//
// Dependencies:
//   - github.com/voocel/litellm — litellm.Message type for conversation messages
//
// Extension points:
//   - Replace sliding-window eviction with a different strategy
//   - Add message persistence (e.g. SQLite) for crash recovery
//   - Expose per-session metadata or tags
package history
