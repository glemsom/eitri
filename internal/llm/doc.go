// Package llm provides the LLM transport abstraction — a lightweight
// interface for multi-provider chat completions, backed by the litellm bridge.
//
// It owns the LLMService contract and the domain types shared across all
// consumers. Wire protocol details are handled by internal/provider via
// litellm, and this package bridges between Eitri's domain types and the
// litellm library's types.
//
// # Key types
//
//   - LLMService — the core interface for chat completions (Chat + ChatStream)
//   - Request — provider-agnostic chat completion request
//   - Response — non-streaming chat completion response
//   - Message — canonical message type used throughout the application (role, content,
//     reasoning content, tool calls, UI metadata)
//   - ToolCall — a function call made by the LLM
//   - FunctionCall — name + JSON arguments of a tool call
//   - ToolDef — tool definition for the LLM (name, description, schema)
//   - Usage — token usage accounting
//   - StreamEvent — one event from a streaming response (token, tool call, done, error)
//   - StreamEventType — enum of stream event kinds
//   - AdapterConfig — per-provider adapter construction parameters
//
// # Key interfaces
//
//   - LLMService — Chat(ctx, Request) → (*Response, error)
//     ChatStream(ctx, Request) → (<-chan StreamEvent, error)
//
// The Bridge (bridge.go) implements LLMService by wrapping a *litellm.Client.
// Consumers that need an LLMService should call provider.NewLitellmClient to
// obtain a litellm client, then wrap it with NewBridge.
//
// # Dependencies
//
// stdlib only — no internal/eitri packages are imported.
package llm
