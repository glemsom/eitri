package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/voocel/litellm"
)

// Bridge implements the LLMService interface by wrapping a *litellm.Client.
// It converts between Eitri's domain types (Request, Response, StreamEvent)
// and litellm's types (litellm.Request, litellm.Response, litellm.Event).
//
// This is a temporary shim used during the migration from hand-rolled
// adapters to litellm.Client. Once all consumers are migrated away from the
// LLMService interface, this file can be deleted.
type Bridge struct {
	client *litellm.Client
}

// NewBridge wraps a *litellm.Client as an LLMService.
func NewBridge(client *litellm.Client) LLMService {
	return &Bridge{client: client}
}

// Chat sends a non-streaming chat completion request.
func (b *Bridge) Chat(ctx context.Context, req Request) (*Response, error) {
	lr, err := toLitellmRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := b.client.Chat(ctx, *lr)
	if err != nil {
		return nil, bridgeError(err)
	}

	return fromLitellmResponse(resp), nil
}

// ChatStream sends a streaming chat completion request.
// Returns a channel of StreamEvent that must be drained until closed.
func (b *Bridge) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	lr, err := toLitellmRequest(req)
	if err != nil {
		return nil, err
	}

	stream, err := b.client.Stream(ctx, *lr)
	if err != nil {
		return nil, bridgeError(err)
	}

	ch := make(chan StreamEvent, 64)
	go b.readStream(ctx, stream, ch)
	return ch, nil
}

// readStream drains a litellm.Stream and sends converted events on ch.
func (b *Bridge) readStream(ctx context.Context, stream litellm.Stream, ch chan<- StreamEvent) {
	defer close(ch)
	defer stream.Close()

	// In-progress tool call accumulation by index (OpenAI-style).
	type pendingTool struct {
		id     string
		name   string
		argBuf strings.Builder
	}
	pending := make(map[int]*pendingTool)

	// FlushReasoning sends buffered reasoning as tokens.
	var reasoningBuf strings.Builder
	flushReasoning := func() {
		if reasoningBuf.Len() > 0 {
			ch <- StreamEvent{
				Type:        StreamEventTypeToken,
				Content:     reasoningBuf.String(),
				IsReasoning: true,
			}
			reasoningBuf.Reset()
		}
	}

	// flushToolCall emits a single tool call as a StreamEvent and removes it from pending.
	flushOneToolCall :=func(idx int) {
		p, ok := pending[idx]
		if !ok {
			return
		}
		delete(pending, idx)
		flushReasoning()
		ch <- StreamEvent{
			Type: StreamEventTypeToolCall,
			ToolCalls: []ToolCall{{
				ID:   p.id,
				Type: "function",
				Function: FunctionCall{
					Name:      p.name,
					Arguments: p.argBuf.String(),
				},
			}},
		}
	}

	// flushAllToolCalls emits all pending tool calls as a single StreamEvent.
	flushAllToolCalls := func() {
		if len(pending) == 0 {
			return
		}
		flushReasoning()
		// Emit in sorted index order for deterministic output.
		indices := make([]int, 0, len(pending))
		for idx := range pending {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		calls := make([]ToolCall, 0, len(indices))
		for _, idx := range indices {
			p := pending[idx]
			calls = append(calls, ToolCall{
				ID:   p.id,
				Type: "function",
				Function: FunctionCall{
					Name:      p.name,
					Arguments: p.argBuf.String(),
				},
			})
		}
		ch <- StreamEvent{
			Type:      StreamEventTypeToolCall,
			ToolCalls: calls,
		}
		pending = make(map[int]*pendingTool) // clear
	}

	var usage *Usage

	for {
		event, err := stream.Next()
		if err != nil {
			flushAllToolCalls()
			flushReasoning()
			ch <- StreamEvent{Type: StreamEventTypeError, Error: bridgeError(err)}
			return
		}

		switch e := event.(type) {
		case litellm.ContentDelta:
			flushReasoning()
			ch <- StreamEvent{
				Type:    StreamEventTypeToken,
				Content: e.Text,
			}

		case litellm.ReasoningDelta:
			reasoningBuf.WriteString(e.Text)

		case litellm.ToolUseStart:
			idx := 0
			if e.Index != nil {
				idx = *e.Index
			}
			if _, exists := pending[idx]; !exists {
				pending[idx] = &pendingTool{
					id:   e.ID,
					name: e.Name,
				}
			}

		case litellm.ToolUseDelta:
			idx := 0
			if e.Index != nil {
				idx = *e.Index
			}
			if p, ok := pending[idx]; ok {
				p.argBuf.Write(e.ArgumentsDelta)
			}

		case litellm.ToolUseDone:
			// Anthropic-style: ToolUseDone signals the end of a tool call.
			idx := 0
			if e.Index != nil {
				idx = *e.Index
			}
			if _, ok := pending[idx]; ok {
				flushOneToolCall(idx)
			} else {
				// No pending start — treat as a complete single-event tool call.
				flushReasoning()
				ch <- StreamEvent{
					Type: StreamEventTypeToolCall,
					ToolCalls: []ToolCall{{
						ID:   e.ID,
						Type: "function",
						Function: FunctionCall{
							Name: "", // Name may be empty if we only have the done
						},
					}},
				}
			}

		case litellm.UsageEvent:
			u := fromLitellmUsage(&e.Usage)
			if u != nil {
				usage = u
			}

		case litellm.DoneEvent:
			flushAllToolCalls()
			flushReasoning()
			ch <- StreamEvent{
				Type:         StreamEventTypeDone,
				FinishReason: string(e.FinishReason),
				Usage:        usage,
			}
			return

		case litellm.ErrorEvent:
			flushAllToolCalls()
			flushReasoning()
			ch <- StreamEvent{Type: StreamEventTypeError, Error: bridgeError(e.Err)}
			return
		}
	}
}

// toLitellmRequest converts an llm.Request to a litellm.Request.
func toLitellmRequest(req Request) (*litellm.Request, error) {
	messages := make([]litellm.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ToLitellmMessage(m))
	}

	tools := make([]litellm.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema, err := litellm.SchemaFrom(t.Parameters)
		if err != nil {
			return nil, fmt.Errorf("convert tool %q schema: %w", t.Name, err)
		}
		tools = append(tools, litellm.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schema,
		})
	}

	lr := &litellm.Request{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
	}

	// Default max_tokens (required by some providers like Anthropic)
	maxTokens := 4096
	lr.MaxTokens = &maxTokens

	// Reasoning effort — only set for models known to support thinking/reasoning.
	// litellm's openai provider validates this and rejects non-reasoning models.
	if req.ReasoningEffort != "" && isReasoningModel(req.Model) {
		lr.Thinking = &litellm.Thinking{
			Mode:   litellm.ThinkingEnabled,
			Effort: req.ReasoningEffort,
		}
	}

	// Prompt cache key via provider options
	if req.SessionID != "" {
		if lr.ProviderOptions == nil {
			lr.ProviderOptions = make(litellm.ProviderOptions)
		}
		// Truncate to 64 chars as per existing behavior
		key := req.SessionID
		if len(key) > 64 {
			key = key[:64]
		}
		lr.ProviderOptions["prompt_cache_key"] = key
	}

	return lr, nil
}

// ToLitellmMessage converts an llm.Message to a litellm.Message.
func ToLitellmMessage(m Message) litellm.Message {
	var blocks []litellm.Block

	// Assistant messages with tool calls get structured blocks
	if m.Role == "assistant" && len(m.ToolCalls) > 0 {
		if m.Content != "" {
			blocks = append(blocks, litellm.TextBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			blocks = append(blocks, litellm.ToolUseBlock{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	} else if m.Role == "tool" {
		blocks = append(blocks, litellm.ToolResultBlock{
			ToolUseID: m.ToolCallID,
			Content:   []litellm.Block{litellm.TextBlock{Text: m.Content}},
		})
	} else {
		// System, user, or simple assistant messages
		content := m.Content
		if m.ReasoningContent != "" {
			content = m.ReasoningContent + "\n" + content
		}
		blocks = append(blocks, litellm.TextBlock{Text: content})
	}

	return litellm.Message{
		Role:   litellm.Role(m.Role),
		Blocks: blocks,
	}
}

// FromLitellmMessage converts a litellm.Message to an llm.Message.
// UI-only fields (CreatedAt, Components, QuickReplies) are zero-valued
// and should be populated by the caller if needed.
func FromLitellmMessage(msg litellm.Message) Message {
	var content, reasoningContent string
	var toolCalls []ToolCall
	var toolCallID string

	for _, block := range msg.Blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			content += b.Text
		case litellm.ReasoningBlock:
			reasoningContent += b.Text
		case litellm.ToolUseBlock:
			args := ""
			if len(b.Arguments) > 0 {
				args = string(b.Arguments)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case litellm.ToolResultBlock:
			toolCallID = b.ToolUseID
			for _, sub := range b.Content {
				if txt, ok := sub.(litellm.TextBlock); ok {
					content += txt.Text
				}
			}
		}
	}

	return Message{
		Role:             string(msg.Role),
		Content:          content,
		ReasoningContent: reasoningContent,
		ToolCallID:       toolCallID,
		ToolCalls:        toolCalls,
	}
}

// fromLitellmResponse converts a litellm.Response to an llm.Response.
func fromLitellmResponse(resp *litellm.Response) *Response {
	out := &Response{
		Content: resp.Text(),
		Usage:   fromLitellmUsage(&resp.Usage),
	}

	// Finish reason
	out.FinishReason = string(resp.FinishReason)
	if out.FinishReason == "" && resp.FinishReasonRaw != "" {
		out.FinishReason = resp.FinishReasonRaw
	}

	// Tool calls
	for _, tc := range resp.ToolCalls() {
		args := ""
		if len(tc.Arguments) > 0 {
			args = string(tc.Arguments)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: FunctionCall{
				Name:      tc.Name,
				Arguments: args,
			},
		})
	}

	return out
}

// fromLitellmUsage converts a litellm.Usage to an llm.Usage.
func fromLitellmUsage(u *litellm.Usage) *Usage {
	if u == nil || !u.HasTokens() {
		return nil
	}
	return &Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// isReasoningModel returns true for models known to support thinking/reasoning
// effort levels in litellm's OpenAI provider. Only gpt-5 is currently
// recognized by litellm as a reasoning model.
func isReasoningModel(model string) bool {
	lower := strings.ToLower(model)
	// Strip any provider prefix (e.g., "openai/gpt-5" -> "gpt-5")
	if _, after, ok := strings.Cut(lower, "/"); ok {
		lower = after
	}
	return strings.HasPrefix(lower, "gpt-5")
}

// bridgeError converts a litellm error to a user-facing error string
// that matches the existing error classification behavior.
func bridgeError(err error) error {
	if err == nil {
		return nil
	}

	// litellm errors are already typed; unwrap them for classification
	errStr := err.Error()
	lower := strings.ToLower(errStr)

	switch {
	case strings.Contains(lower, "connection refused"):
		return fmt.Errorf("connection refused: provider is not reachable. Check that your LLM provider is running and accessible")
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return fmt.Errorf("request timed out. The provider took too long to respond")
	case strings.Contains(lower, "status 401") || strings.Contains(lower, "401:") || strings.Contains(lower, "auth") || strings.Contains(lower, "authentication"):
		return fmt.Errorf("Authentication failed (401). Check your API key")
	case strings.Contains(lower, "status 429") || strings.Contains(lower, "429:") || strings.Contains(lower, "rate limit"):
		return fmt.Errorf("Rate limited (429). Try again later")
	case strings.Contains(lower, "context_length") || strings.Contains(lower, "context length"):
		return fmt.Errorf("Context length exceeded. Your message is too long for the selected model")
	case strings.Contains(lower, "status 5") || strings.Contains(lower, "5") && strings.Contains(lower, "server"):
		return fmt.Errorf("Server error: provider returned an error")
	default:
		return fmt.Errorf("LLM request failed: %w", err)
	}
}
