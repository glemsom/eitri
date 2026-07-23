// Package litellm provides the LLM transport abstraction — a lightweight
// interface and adapter factory for multi-provider chat completions.
//
// It owns the LLMService contract, wire-type conversion, streaming SSE
// parsing, HTTP error classification, and adapter implementations for
// OpenAI-compatible, Anthropic Messages API, OpenRouter, and GitHub Copilot.
//
// # Key types
//
//   - LLMService — the core interface for chat completions (Chat + ChatStream)
//   - Request — provider-agnostic chat completion request
//   - Response — non-streaming chat completion response
//   - Message — one message in the conversation (role, content, tool calls)
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
//     Implementations wrap a specific provider wire protocol
//     (OpenAI-compatible JSON, Anthropic Messages API SSE, etc.).
//
// # Domain types vs wire types
//
// The Request/Response/Message types are domain types shared across all
// adapters. Wire types (openAIReq, openAIResp, anthropicReq, anthropicResp,
// etc.) live in wire_types.go and are converted via to* / from* helper
// functions. This keeps provider protocol details out of the caller's
// concern and makes adding a new wire format a local change.
//
// # Adapter implementations
//
//   - OpenAI — openAICompatible struct (openai.go); shared by opencode_go,
//     custom_openai, OpenRouter (via wrapper), GitHub Copilot (via wrapper)
//   - Anthropic — Anthropic struct (anthropic.go); used by opencode_go with
//     qwen*/minimax* models
//   - OpenRouter — wraps openAICompatible with HTTP-Referer and X-Title
//   - GitHub Copilot — wraps openAICompatible with Copilot-specific headers
//     and /chat/completions path
//
// # Dependencies
//
// stdlib only — no internal/eitri packages are imported.
//
// # Extension points
//
//  1. Add a new provider adapter:
//     a) Create a file (e.g., myprovider.go) with a struct implementing LLMService.
//     b) Add a NewMyProvider constructor using the shared HTTP helpers in common.go.
//     c) Register the route in NewLLMService (factory.go).
//
//  2. Add a new wire format:
//     a) Add wire types in wire_types.go.
//     b) Add conversion helpers (toWire, fromWire).
//     c) Wire the adapter in a new struct or extend an existing one.
//
//  3. Extend the domain types:
//     Add fields to Request, Response, or Message. Wire-type conversion
//     helpers may need corresponding updates.
package litellm
