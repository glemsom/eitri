package loop

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runstate"
)

// drainStream reads all events from a stream channel and collects text content
// and tool calls. Token events are forwarded to the SSE writer.
// Returns the accumulated content, any tool calls, the Usage from the Done event
// (may be nil if the provider did not send usage data), and any error.
func drainStream(
	ctx context.Context,
	stream <-chan llm.StreamEvent,
	sseWriter *runstate.Writer,
) (*strings.Builder, []llm.ToolCall, *llm.Usage, error) {
	var content strings.Builder
	var toolCalls []llm.ToolCall
	var usage *llm.Usage

	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return &content, toolCalls, usage, nil
			}

			switch evt.Type {
			case llm.StreamEventTypeToken:
				if evt.IsReasoning {
					// Reasoning content from adapters is clean text — the IsReasoning
					// flag is the sole discriminator (no delimiter tags expected).
					sseWriter.ThinkingDelta(evt.Content)
				} else {
					content.WriteString(evt.Content)
					sseWriter.Token(evt.Content)
				}

			case llm.StreamEventTypeToolCall:
				if len(evt.ToolCalls) > 0 {
					toolCalls = evt.ToolCalls
				}

			case llm.StreamEventTypeDone:
				usage = evt.Usage
				return &content, toolCalls, usage, nil

			case llm.StreamEventTypeError:
				if evt.Error != nil {
					return &content, toolCalls, usage, evt.Error
				}
				return &content, toolCalls, usage, nil
			}

		case <-ctx.Done():
			// Before returning with cancellation error, drain any buffered events
			// from the stream. The channel may already have events queued (e.g.
			// when context was cancelled concurrently with a token send on a
			// buffered channel). Go's select picks randomly when multiple cases
			// are ready, so we must check explicitly.
			for {
				select {
				case evt, ok := <-stream:
					if !ok {
						return &content, toolCalls, usage, ctx.Err()
					}
					switch evt.Type {
					case llm.StreamEventTypeToken:
						if evt.IsReasoning {
							sseWriter.ThinkingDelta(evt.Content)
						} else {
							content.WriteString(evt.Content)
							sseWriter.Token(evt.Content)
						}
					case llm.StreamEventTypeToolCall:
						if len(evt.ToolCalls) > 0 {
							toolCalls = evt.ToolCalls
						}
					case llm.StreamEventTypeDone:
						usage = evt.Usage
						return &content, toolCalls, usage, ctx.Err()
					case llm.StreamEventTypeError:
						if evt.Error != nil {
							return &content, toolCalls, usage, evt.Error
						}
						return &content, toolCalls, usage, ctx.Err()
					}
				default:
					// No more buffered events — proceed with cancellation
					return &content, toolCalls, usage, ctx.Err()
				}
			}
		}
	}
}
