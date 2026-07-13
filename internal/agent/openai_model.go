package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

// OpenAIModel implements model.LLM for OpenAI-compatible chat completions.
type OpenAIModel struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenAIModel creates an OpenAI-compatible model.LLM.
func NewOpenAIModel(name, baseURL, apiKey string) *OpenAIModel {
	return &OpenAIModel{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (m *OpenAIModel) Name() string { return m.name }

// ————— wire types —————

type openAIReq struct {
	Model      string      `json:"model"`
	Messages   []openAIMsg `json:"messages"`
	Stream     bool        `json:"stream"`
	Tools      interface{} `json:"tools,omitempty"`
	ToolChoice string      `json:"tool_choice,omitempty"`
}

type openAIMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
}

type openAIRespChunk struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageInfo `json:"usage,omitempty"`
	Error *apiError  `json:"error,omitempty"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ————— model.LLM —————

func (m *OpenAIModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		openAIReq := m.toOpenAIReq(req, stream)
		body, err := json.Marshal(openAIReq)
		if err != nil {
			yield(nil, fmt.Errorf("failed to marshal request: %w", err))
			return
		}

		endpoint := m.baseURL + "/v1/chat/completions"
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			yield(nil, fmt.Errorf("failed to create request: %w", err))
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		if m.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
		}

		resp, err := m.client.Do(httpReq)
		if err != nil {
			yield(nil, m.classifyNetError(err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			yield(nil, m.parseHTTPError(resp))
			return
		}

		m.readStream(resp.Body, yield)
	}
}

// ————— request builder —————

func (m *OpenAIModel) toOpenAIReq(req *model.LLMRequest, stream bool) *openAIReq {
	var msgs []openAIMsg
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		msg := openAIMsg{Role: mapRole(c.Role)}
		var textParts []string
		for _, part := range c.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
			if call := part.FunctionCall; call != nil {
				msg.Role = "assistant"
				argsJSON, _ := json.Marshal(call.Args)
				msg.ToolCalls = mustMarshal([]map[string]any{
					{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(argsJSON)}},
				})
			}
			if fr := part.FunctionResponse; fr != nil {
				msg.Role = "tool"
				msg.ToolCallID = fr.ID
				respJSON, _ := json.Marshal(fr.Response)
				textParts = append(textParts, string(respJSON))
			}
		}
		if len(textParts) > 0 {
			msg.Content = strings.Join(textParts, "\n")
		}
		msgs = append(msgs, msg)
	}

	var tools interface{}
	if len(req.Tools) > 0 {
		tools = m.buildToolDefs(req.Tools)
	}

	return &openAIReq{
		Model:    m.name,
		Messages: msgs,
		Stream:   stream,
		Tools:    tools,
	}
}

func mapRole(r string) string {
	switch r {
	case "user":
		return "user"
	case "model":
		return "assistant"
	default:
		return r
	}
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

type toolDef struct {
	Type     string   `json:"type"`
	Function toolFunc `json:"function"`
}

type toolFunc struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

func (m *OpenAIModel) buildToolDefs(tools map[string]any) []toolDef {
	var defs []toolDef
	for _, t := range tools {
		d := toolDef{Type: "function"}
		if named, ok := t.(interface{ Name() string }); ok {
			d.Function.Name = named.Name()
		}
		if desc, ok := t.(interface{ Description() string }); ok {
			d.Function.Description = desc.Description()
		}
		// Try to extract JSON schema from functiontool struct tags
		if schema, ok := t.(interface{ JSONSchema() any }); ok {
			d.Function.Parameters = schema.JSONSchema()
		}
		defs = append(defs, d)
	}
	return defs
}

// ————— streaming reader —————

func (m *OpenAIModel) readStream(body io.Reader, yield func(*model.LLMResponse, error) bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 65536), 1<<20)

	buf := &streamBuf{}

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
			break
		}

		var chunk openAIRespChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("bad SSE chunk: %v", err)
			continue
		}

		if chunk.Error != nil {
			yield(nil, fmt.Errorf("LLM error: %s", chunk.Error.Message))
			return
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// Accumulate text + tool call fragments
		buf.addContent(choice.Delta.Content)
		if len(choice.Delta.ToolCalls) > 0 {
			buf.addToolCalls(choice.Delta.ToolCalls)
		}

		// Yield partial text delta
		if choice.Delta.Content != "" {
			if !yield(&model.LLMResponse{
				Content: &genai.Content{Parts: []*genai.Part{{Text: choice.Delta.Content}}},
				Partial: true,
			}, nil) {
				return
			}
		}

		if choice.FinishReason != "" {
			final := buf.finalize(genai.FinishReasonStop)
			if final == nil {
				break
			}
			final.TurnComplete = true
			if chunk.Usage != nil {
				final.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     int32(chunk.Usage.PromptTokens),
					CandidatesTokenCount: int32(chunk.Usage.CompletionTokens),
					TotalTokenCount:      int32(chunk.Usage.TotalTokens),
				}
			}
			yield(final, nil)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		yield(nil, fmt.Errorf("SSE read error: %w", err))
		return
	}

	// Provider never sent finish_reason – synthesize final
	if final := buf.finalize(genai.FinishReasonStop); final != nil {
		final.TurnComplete = true
		yield(final, nil)
	}
}

// ————— stream buffer (fragment assembly) —————

type streamBuf struct {
	mu   sync.Mutex
	text strings.Builder
	tcs  []map[string]any
}

func (b *streamBuf) addContent(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text.WriteString(s)
}

func (b *streamBuf) addToolCalls(raw json.RawMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var incoming []map[string]any
	if err := json.Unmarshal(raw, &incoming); err != nil {
		log.Printf("bad tool_calls in stream: %v", err)
		return
	}
	for _, tc := range incoming {
		idx := 0
		if i, ok := tc["index"].(float64); ok {
			idx = int(i)
		}
		for idx >= len(b.tcs) {
			b.tcs = append(b.tcs, nil)
		}
		if b.tcs[idx] == nil {
			b.tcs[idx] = tc
			continue
		}
		// Merge fragmented function arguments
		existing := b.tcs[idx]
		if ef, ok := existing["function"].(map[string]any); ok {
			if nf, ok2 := tc["function"].(map[string]any); ok2 {
				if ea, ok3 := ef["arguments"].(string); ok3 {
					if na, ok4 := nf["arguments"].(string); ok4 {
						ef["arguments"] = ea + na
					}
				}
				// Copy name if not set yet
				if ef["name"] == nil || ef["name"] == "" {
					ef["name"] = nf["name"]
				}
			}
		}
	}
}

func (b *streamBuf) finalize(why genai.FinishReason) *model.LLMResponse {
	b.mu.Lock()
	defer b.mu.Unlock()

	content := b.text.String()
	var parts []*genai.Part
	if content != "" {
		parts = append(parts, &genai.Part{Text: content})
	}
	for _, tc := range b.tcs {
		if tc == nil {
			continue
		}
		fn, _ := tc["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		argsStr, _ := fn["arguments"].(string)
		if name == "" {
			continue
		}
		var args map[string]any
		json.Unmarshal([]byte(argsStr), &args)
		parts = append(parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				Name: name,
				Args: args,
			},
		})
	}

	return &model.LLMResponse{
		Content: &genai.Content{Role: "model", Parts: parts},
	}
}

// ————— error helpers —————

func (m *OpenAIModel) classifyNetError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("connection refused: provider at %s is not reachable. Check that your LLM provider is running and accessible", m.baseURL)
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return fmt.Errorf("request timed out. The provider took too long to respond")
	default:
		return fmt.Errorf("LLM request failed: %w", err)
	}
}

type openAIErrorResponse struct {
	Error apiError `json:"error"`
}

func (m *OpenAIModel) parseHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp openAIErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		msg := strings.ToLower(errResp.Error.Message)
		switch {
		case resp.StatusCode == 401:
			return fmt.Errorf("Authentication failed (401). Check your API key")
		case resp.StatusCode == 429:
			return fmt.Errorf("Rate limited (429). The provider returned: %s", errResp.Error.Message)
		case resp.StatusCode == 400 && (strings.Contains(msg, "context_length") || strings.Contains(msg, "context length")):
			return fmt.Errorf("Context length exceeded. Your message is too long for the selected model")
		default:
			return fmt.Errorf("Provider returned HTTP %d: %s", resp.StatusCode, errResp.Error.Message)
		}
	}
	return fmt.Errorf("Provider returned HTTP %d", resp.StatusCode)
}
