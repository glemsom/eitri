package litellm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAI implements LLMService for OpenAI-compatible chat completion APIs.
type OpenAI struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenAI creates an OpenAI-compatible adapter.
func NewOpenAI(model, baseURL, apiKey string) *OpenAI {
	return &OpenAI{
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  defaultHTTPClient,
	}
}

func (s *OpenAI) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = false

	resp, err := doChatRequest[openAIReq, openAIResp](ctx, s.client, s.baseURL+"/v1/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
	})
	if err != nil {
		return nil, err
	}

	return fromOpenAIResponse(*resp), nil
}

func (s *OpenAI) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true

	resp, err := doChatStreamRequest(ctx, s.client, s.baseURL+"/v1/chat/completions", wireReq, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.apiKey)
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go readOpenAIStream(ctx, resp.Body, ch)
	return ch, nil
}

// readOpenAIStream reads an SSE stream from an OpenAI-compatible API.
func readOpenAIStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 65536), 1<<20)

	// Accumulate tool call fragments across chunks
	toolCallBuf := make(map[int]*openAIToolCall) // index -> accumulated tool call

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
			ch <- StreamEvent{Type: StreamEventTypeDone}
			return
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- StreamEvent{
				Type:  StreamEventTypeError,
				Error: fmt.Errorf("failed to parse stream chunk: %w", err),
			}
			return
		}

		if chunk.Error != nil {
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

		if choice.Delta.Content != "" {
			ch <- StreamEvent{
				Type:    StreamEventTypeToken,
				Content: choice.Delta.Content,
			}
		}

		if len(choice.Delta.ToolCalls) > 0 {
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
			ch <- StreamEvent{
				Type:         StreamEventTypeDone,
				FinishReason: choice.FinishReason,
				Usage:        chunk.Usage,
			}
			return
		}
	}

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
