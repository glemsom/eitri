package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultHTTPClient is reused across all adapters.
var defaultHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// debugLogPayload dumps full LLM request JSON to slog.Info when set.
// Enable via EITRI_DEBUG_PROMPT=1 or EITRI_DEBUG_REQUEST=1 env var.
var debugLogPayload = os.Getenv("EITRI_DEBUG_PROMPT") == "1" || os.Getenv("EITRI_DEBUG_REQUEST") == "1"

// doRequest sends the HTTP request and returns the response.
func doRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyNetError(err)
	}
	return resp, nil
}

// classifyNetError maps network errors to user-facing messages.
func classifyNetError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("connection refused: provider is not reachable. Check that your LLM provider is running and accessible")
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return fmt.Errorf("request timed out. The provider took too long to respond")
	default:
		return fmt.Errorf("LLM request failed: %w", err)
	}
}

// readAll reads and closes the response body.
func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// classifyHTTPError creates a user-facing error from an HTTP error response.
func classifyHTTPError(statusCode int, body []byte) error {
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	var msg string
	if len(body) > 0 {
		if err := json.Unmarshal(body, &errBody); err == nil && errBody.Error.Message != "" {
			msg = errBody.Error.Message
		}
	}

	lower := strings.ToLower(msg)
	switch {
	case statusCode == 401:
		if msg != "" {
			return fmt.Errorf("Authentication failed (401): %s", msg)
		}
		return fmt.Errorf("Authentication failed (401). Check your API key")
	case statusCode == 429:
		if msg != "" {
			return fmt.Errorf("Rate limited (429): %s", msg)
		}
		return fmt.Errorf("Rate limited (429). Try again later")
	case statusCode == 400 && (strings.Contains(lower, "context_length") || strings.Contains(lower, "context length")):
		return fmt.Errorf("Context length exceeded. Your message is too long for the selected model")
	case statusCode >= 500:
		if msg != "" {
			return fmt.Errorf("Server error (%d): %s", statusCode, msg)
		}
		return fmt.Errorf("Server error (%d). The provider encountered an error", statusCode)
	default:
		if msg != "" {
			return fmt.Errorf("Provider returned HTTP %d: %s", statusCode, msg)
		}
		return fmt.Errorf("Provider returned HTTP %d", statusCode)
	}
}

// ————— wire types for OpenAI-compatible API —————

type openAIReq struct {
	Model    string      `json:"model"`
	Messages []openAIMsg `json:"messages"`
	Stream   bool        `json:"stream"`
	Tools    interface{} `json:"tools,omitempty"`
}

type openAIMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function openAIFuncCall `json:"function"`
}

type openAIFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResp struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
	Error   *openAIAPIError `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIRespMsg `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIRespMsg struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIChunk struct {
	Choices []openAIChunkChoice `json:"choices"`
	Usage   *Usage              `json:"usage,omitempty"`
	Error   *openAIAPIError     `json:"error,omitempty"`
}

type openAIChunkChoice struct {
	Index        int              `json:"index"`
	Delta        openAIChunkDelta `json:"delta"`
	FinishReason string           `json:"finish_reason"`
}

type openAIChunkDelta struct {
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ————— conversion helpers —————

func toOpenAIRequest(req Request) openAIReq {
	msgs := make([]openAIMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openAIMsg{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]openAIToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openAIFuncCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		msgs = append(msgs, msg)
	}
	return openAIReq{
		Model:    req.Model,
		Messages: msgs,
		Stream:   req.Stream,
	}
}

func fromOpenAIResponse(resp openAIResp) *Response {
	if len(resp.Choices) == 0 {
		return &Response{Usage: resp.Usage}
	}
	ch := resp.Choices[0]
	out := &Response{
		Content:      ch.Message.Content,
		FinishReason: ch.FinishReason,
		Usage:        resp.Usage,
	}
	for _, tc := range ch.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return out
}

// ————— Anthropic wire types —————

type anthropicReq struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []anthropicMsg  `json:"messages"`
	System    string          `json:"system,omitempty"`
	Stream    bool            `json:"stream"`
}

type anthropicMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicResp struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ————— SSE scanner —————

type sseScanner struct {
	body  io.Reader
	buf   []byte
	event string
	data  string
	done  bool
}

func newSSEScanner(body io.Reader) *sseScanner {
	return &sseScanner{
		body: body,
		buf:  make([]byte, 0, 4096),
	}
}

func (s *sseScanner) Scan() bool {
	if s.done {
		return false
	}
	s.event = ""
	s.data = ""

	tmp := make([]byte, 1)
	for {
		n, err := s.body.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[0])
			if tmp[0] == '\n' && len(s.buf) >= 2 {
				// Check if we have a blank line (double newline = event boundary)
				ends := string(s.buf)
				if strings.HasSuffix(strings.TrimRight(ends, "\r"), "\n\n") {
					break
				}
			}
		}
		if err != nil {
			s.done = true
			break
		}
	}

	if len(s.buf) == 0 {
		return false
	}

	lines := strings.Split(string(s.buf), "\n")
	s.buf = s.buf[:0]

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			s.event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			s.data = strings.TrimPrefix(line, "data: ")
		}
	}

	return true
}

func (s *sseScanner) Event() string { return s.event }
func (s *sseScanner) Data() string  { return s.data }

// ————— shared HTTP request helpers —————

// doChatRequest is a generic helper for non-streaming chat requests.
// It marshals req to JSON, sends an HTTP POST, reads the full response body,
// and unmarshals the JSON into a new *Resp.
//
// setHeaders is called after Content-Type and Accept are set so the adapter
// can add auth, tracking, or other headers.
func doChatRequest[Req, Resp any](ctx context.Context, client *http.Client, url string, req Req, setHeaders func(*http.Request)) (*Resp, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	setHeaders(httpReq)

	if debugLogPayload {
		slog.Info("llm request",
			slog.String("endpoint", url),
			slog.String("body", string(body)),
		)
	}
	resp, err := doRequest(ctx, client, httpReq)
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

	var wireResp Resp
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &wireResp, nil
}

// doChatStreamRequest is a generic helper for streaming chat requests.
// It marshals req to JSON, sends an HTTP POST with Accept: text/event-stream,
// checks the response status, and returns the raw *http.Response.
// The caller must start a goroutine to read the stream from resp.Body.
//
// setHeaders is called after Content-Type and Accept are set.
func doChatStreamRequest[Req any](ctx context.Context, client *http.Client, url string, req Req, setHeaders func(*http.Request)) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	setHeaders(httpReq)

	if debugLogPayload {
		slog.Info("llm request",
			slog.String("endpoint", url),
			slog.String("body", string(body)),
		)
	}
	resp, err := doRequest(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		return nil, classifyHTTPError(resp.StatusCode, body)
	}

	return resp, nil
}
