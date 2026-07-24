package llm

import "encoding/json"

// ————— wire types for OpenAI-compatible API —————

type openAIReq struct {
	Model           string      `json:"model"`
	Messages        []openAIMsg `json:"messages"`
	Stream          bool        `json:"stream"`
	Tools           any         `json:"tools,omitempty"`
	ReasoningEffort string      `json:"reasoning_effort,omitempty"`
	PromptCacheKey  string      `json:"prompt_cache_key,omitempty"`
}

type openAIMsg struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
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
	Choices []openAIChoice  `json:"choices"`
	Usage   *Usage          `json:"usage,omitempty"`
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
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
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

	// Convert tool definitions to wire format
	var tools []map[string]any
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	// Truncate SessionID to 64 chars for prompt_cache_key
	cacheKey := req.SessionID
	if len(cacheKey) > 64 {
		cacheKey = cacheKey[:64]
	}

	return openAIReq{
		Model:           req.Model,
		Messages:        msgs,
		Stream:          req.Stream,
		Tools:           tools,
		ReasoningEffort: req.ReasoningEffort,
		PromptCacheKey:  cacheKey,
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
	Tools     []anthropicTool `json:"tools,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
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
