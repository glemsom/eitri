package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Anthropic implements LLMService for Anthropic Messages API.
type Anthropic struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewAnthropic creates an Anthropic-compatible adapter.
func NewAnthropic(model, baseURL, apiKey string) *Anthropic {
	return &Anthropic{
		model:   model,
		baseURL: strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1"),
		apiKey:  apiKey,
		client:  defaultHTTPClient,
	}
}

const anthropicDefaultMaxTokens = 4096

func (s *Anthropic) anthropicHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (s *Anthropic) toAnthropicReq(req Request, stream bool) anthropicReq {
	var system string
	var msgs []anthropicMsg

	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "assistant"
		}

		content := s.messageToAnthropicContent(m)
		msgs = append(msgs, anthropicMsg{
			Role:    role,
			Content: content,
		})
	}

	return anthropicReq{
		Model:     s.model,
		MaxTokens: anthropicDefaultMaxTokens,
		System:    system,
		Messages:  msgs,
		Stream:    stream,
	}
}

func (s *Anthropic) messageToAnthropicContent(msg Message) json.RawMessage {
	if len(msg.ToolCalls) > 0 {
		blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls)+1)
		if msg.Content != "" {
			blocks = append(blocks, anthropicContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
			blocks = append(blocks, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: args,
			})
		}
		b, _ := json.Marshal(blocks)
		return b
	}

	if msg.ToolCallID != "" {
		blocks := []anthropicContentBlock{{
			Type:      "tool_result",
			ToolUseID: msg.ToolCallID,
			Content:   msg.Content,
		}}
		b, _ := json.Marshal(blocks)
		return b
	}

	b, _ := json.Marshal(msg.Content)
	return b
}

func (s *Anthropic) fromAnthropicResponse(resp anthropicResp) *Response {
	out := &Response{
		FinishReason: mapAnthropicStopReason(resp.StopReason),
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		out.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			out.Content += block.Text
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		}
	}
	return out
}

func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	default:
		return reason
	}
}

func (s *Anthropic) Chat(ctx context.Context, req Request) (*Response, error) {
	wireReq := s.toAnthropicReq(req, false)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	s.anthropicHeaders(httpReq)

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

	var anthropicResp anthropicResp
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return s.fromAnthropicResponse(anthropicResp), nil
}

func (s *Anthropic) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	wireReq := s.toAnthropicReq(req, true)
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	s.anthropicHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := doRequest(ctx, s.client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		return nil, classifyHTTPError(resp.StatusCode, body)
	}

	ch := make(chan StreamEvent, 64)
	go readAnthropicStream(ctx, resp.Body, ch)
	return ch, nil
}

func readAnthropicStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := newSSEScanner(body)

	var thinkingBuf strings.Builder
	flushThinking := func() {
		if thinkingBuf.Len() > 0 {
			ch <- StreamEvent{
				Type:        StreamEventTypeToken,
				Content:     "<think>" + thinkingBuf.String() + "</think>",
				IsReasoning: true,
			}
			thinkingBuf.Reset()
		}
	}

	for scanner.Scan() {
		event := scanner.Event()
		data := scanner.Data()

		switch event {
		case "message_start":
		case "content_block_start":
			var block struct {
				Index       int                  `json:"index"`
				ContentBlock anthropicContentBlock `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &block); err != nil {
				continue
			}
			// Flush buffered thinking before any non-thinking content block
			if block.ContentBlock.Type == "tool_use" {
				flushThinking()
				inputJSON, _ := json.Marshal(block.ContentBlock.Input)
				ch <- StreamEvent{
					Type: StreamEventTypeToolCall,
					ToolCalls: []ToolCall{{
						ID:   block.ContentBlock.ID,
						Type: "function",
						Function: FunctionCall{
							Name:      block.ContentBlock.Name,
							Arguments: string(inputJSON),
						},
					}},
				}
			}
			if block.ContentBlock.Type == "text" {
				flushThinking()
			}

		case "content_block_delta":
			var delta struct {
				Index int `json:"index"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				continue
			}
			if delta.Delta.Type == "thinking_delta" && delta.Delta.Text != "" {
				thinkingBuf.WriteString(delta.Delta.Text)
			}
			if delta.Delta.Type == "text_delta" && delta.Delta.Text != "" {
				ch <- StreamEvent{
					Type:    StreamEventTypeToken,
					Content: delta.Delta.Text,
				}
			}

		case "message_delta":
			var delta struct {
				Delta struct {
					StopReason   string `json:"stop_reason"`
					StopSequence string `json:"stop_sequence"`
				} `json:"delta"`
				Usage anthropicUsage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				continue
			}
			if delta.Delta.StopReason != "" {
				flushThinking()
				usage := &Usage{}
				if delta.Usage.InputTokens > 0 || delta.Usage.OutputTokens > 0 {
					usage = &Usage{
						PromptTokens:     delta.Usage.InputTokens,
						CompletionTokens: delta.Usage.OutputTokens,
						TotalTokens:      delta.Usage.InputTokens + delta.Usage.OutputTokens,
					}
				}
				ch <- StreamEvent{
					Type:         StreamEventTypeDone,
					FinishReason: mapAnthropicStopReason(delta.Delta.StopReason),
					Usage:        usage,
				}
				return
			}

		case "message_stop":
			flushThinking()
			return

		case "error":
			flushThinking()
			ch <- StreamEvent{
				Type:  StreamEventTypeError,
				Error: fmt.Errorf("Anthropic error: %s", data),
			}
			return
		}
	}

	flushThinking()
	ch <- StreamEvent{Type: StreamEventTypeDone}
}
