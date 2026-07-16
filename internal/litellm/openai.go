package litellm

import (
	"bufio"
	"bytes"
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
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := doRequest(ctx, s.client, httpReq)
	if err != nil {
		return nil, err
	}

	respBody, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, classifyHTTPError(resp.StatusCode, respBody)
	}

	var openAIResp openAIResp
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return fromOpenAIResponse(openAIResp), nil
}

func (s *OpenAI) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := toOpenAIRequest(req)
	wireReq.Stream = true
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := doRequest(ctx, s.client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		return nil, classifyHTTPError(resp.StatusCode, body)
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
			for i := 0; i < len(toolCallBuf); i++ {
				if tc, ok := toolCallBuf[i]; ok {
					tcs = append(tcs, ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				}
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
