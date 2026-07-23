package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// openAICompatible is a reusable base for any OpenAI-compatible chat provider.
// It implements LLMService by calling doChatRequest/doChatStreamRequest with
// a pluggable chatPath and setHeaders function.
type openAICompatible struct {
	model       string
	baseURL     string
	apiKey      string
	chatPath    string
	setHeaders  func(*http.Request)
	client      *http.Client
}

func (s *openAICompatible) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = false

	resp, err := doChatRequest[openAIReq, openAIResp](ctx, s.client, s.baseURL+s.chatPath, wireReq, s.setHeaders)
	if err != nil {
		return nil, err
	}

	return fromOpenAIResponse(*resp), nil
}

func (s *openAICompatible) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true

	resp, err := doChatStreamRequest(ctx, s.client, s.baseURL+s.chatPath, wireReq, s.setHeaders)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go readOpenAIStream(ctx, resp.Body, ch)
	return ch, nil
}

// NewOpenAI creates an OpenAI-compatible adapter (OpenCode Go route).
func NewOpenAI(model, baseURL, apiKey string, rt http.RoundTripper) LLMService {
	return &openAICompatible{
		model:    model,
		baseURL:  strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1"),
		apiKey:   apiKey,
		chatPath: "/v1/chat/completions",
		setHeaders: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+apiKey)
		},
		client: makeHTTPClient(rt),
	}
}

// readOpenAIStream reads an SSE stream from an OpenAI-compatible API.
func readOpenAIStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 65536), 1<<20)

	// Accumulate tool call fragments across chunks
	toolCallBuf := make(map[int]*openAIToolCall) // index -> accumulated tool call

	// Buffer for reasoning content deltas
	var reasoningBuf strings.Builder

	// flushReasoning sends buffered reasoning as a single <think>...</think> token.
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

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			flushReasoning()
			ch <- StreamEvent{Type: StreamEventTypeDone}
			return
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			flushReasoning()
			ch <- StreamEvent{
				Type:  StreamEventTypeError,
				Error: fmt.Errorf("failed to parse stream chunk: %w", err),
			}
			return
		}

		if chunk.Error != nil {
			flushReasoning()
			ch <- StreamEvent{
				Type:  StreamEventTypeError,
				Error: fmt.Errorf("LLM error: %s", chunk.Error.Message),
			}
			return
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.ReasoningContent != "" {
			reasoningBuf.WriteString(choice.Delta.ReasoningContent)
		}
		if choice.Delta.Content != "" {
			// Flush any buffered reasoning before regular content
			flushReasoning()
			ch <- StreamEvent{
				Type:    StreamEventTypeToken,
				Content: choice.Delta.Content,
			}
		}

		if len(choice.Delta.ToolCalls) > 0 {
			// Flush any buffered reasoning before tool calls so the
			// thinking card appears before tool results in the UI.
			flushReasoning()
			for _, tc := range choice.Delta.ToolCalls {
				idx := tc.Index
				if existing, ok := toolCallBuf[idx]; ok {
					// Merge fragment into existing
					existing.Function.Arguments += tc.Function.Arguments
					if existing.ID == "" && tc.ID != "" {
						existing.ID = tc.ID
					}
					if existing.Type == "" && tc.Type != "" {
						existing.Type = tc.Type
					}
					if existing.Function.Name == "" && tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
				} else {
					toolCallBuf[idx] = &openAIToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: openAIFuncCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
				}
			}

			tcs := make([]ToolCall, 0, len(toolCallBuf))
			for _, tc := range toolCallBuf {
				tcs = append(tcs, ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
			ch <- StreamEvent{
				Type:      StreamEventTypeToolCall,
				ToolCalls: tcs,
			}
		}

		if choice.FinishReason != "" {
			flushReasoning()
			ch <- StreamEvent{
				Type:         StreamEventTypeDone,
				FinishReason: choice.FinishReason,
				Usage:        chunk.Usage,
			}
			return
		}
	}

	flushReasoning()

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{
			Type:  StreamEventTypeError,
			Error: fmt.Errorf("SSE read error: %w", err),
		}
		return
	}

	ch <- StreamEvent{
		Type:  StreamEventTypeError,
		Error: fmt.Errorf("provider did not return SSE chat completions"),
	}
}
