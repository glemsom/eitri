package llm

import "context"

// LLMService is the core abstraction for LLM chat completions.
// Implementations wrap a specific provider wire protocol
// (OpenAI-compatible, Anthropic Messages, etc.).
type LLMService interface {
	// Chat sends a non-streaming chat completion request and returns the full response.
	Chat(ctx context.Context, req Request) (*Response, error)

	// ChatStream sends a streaming chat completion request.
	// Returns a channel of StreamEvent that must be drained until closed.
	ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
