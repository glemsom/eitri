package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAICompatible is a Chat-Completions HTTP client targeting any OpenAI-
// compatible endpoint (OpenCode Go primary provider, docs/research/
// opencode-endpoints.md §5). It serializes the request with model + messages
// and streams the SSE response through the provider seam.
type OpenAICompatible struct {
	apiKey string
	url    string
	http   *http.Client
}

// OpenAICompatible returns a client for the given Bearer key and base URL
// (the full /v1/chat/completions endpoint or a prefix to which it appends).
func NewOpenAICompatible(apiKey, url string) *OpenAICompatible {
	return &OpenAICompatible{apiKey: apiKey, url: url}
}

// Models implements ModelLister: it GETs the provider's /models endpoint and
// returns the list of available model IDs (eitri.md §2.2, T12). The models URL
// is derived from the Chat-Completions endpoint by stripping the
// /chat/completions suffix (research/opencode-endpoints.md §3), the response
// shape being the OpenAI-standard {"data":[{"id":...}]}.
func (o *OpenAICompatible) Models(ctx context.Context) ([]string, error) {
	base := strings.TrimSuffix(o.url, "/chat/completions")
	modelsURL := strings.TrimSuffix(base, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	client := o.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Code: resp.StatusCode, Body: "provider model discovery returned non-2xx"}
	}
	var out modelList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// modelList is the OpenAI-standard model-discovery response shape; only the id
// fields are surfaced today.
type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Stream implements Provider with an HTTP Chat-Completions request.
func (o *OpenAICompatible) Stream(ctx context.Context, req Request) (Stream, error) {
	body, err := json.Marshal(chatCompletionBody{
		Model:      req.Model,
		Messages:   req.Messages,
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		Stream:     true,
		StreamOptions: &streamOptions{
			IncludeUsage: true, // opencode force-sets include_usage (research §4)
		},
		PromptCacheKey:  promptCacheKey(req),
		Thinking:        thinkingControl(req),
		ReasoningEffort: NormalizeReasoningEffort(req.ReasoningEffort),
		MaxOutputTokens: maxOutputTokens(req),
		ResponseFormat:  jsonObjectModeControl(req),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	client := o.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: "provider returned non-2xx"}
	}
	return &openAIStream{ev: newSSE(resp.Body), acc: newToolAccumulator()}, nil
}

// chatCompletionBody is the OpenAI Chat-Completions request shape.
type chatCompletionBody struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []Tool           `json:"tools,omitempty"`
	ToolChoice      any              `json:"tool_choice,omitempty"`
	Stream          bool             `json:"stream"`
	StreamOptions   *streamOptions   `json:"stream_options,omitempty"`
	PromptCacheKey  string           `json:"prompt_cache_key,omitempty"`
	Thinking        *thinkingEnabler `json:"thinking,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	// MaxOutputTokens is the wire-backed Generation Budget (issue #60): a hard
	// per-turn output cap emitted as max_completion_tokens for special turns.
	// Zero (the ordinary-turn default) omits the field so the byte-identical
	// request head is never perturbed (docs/spec.md §4).
	MaxOutputTokens int `json:"max_completion_tokens,omitempty"`
	// ResponseFormat is the wire-backed JSON Object Mode (issue #59): non-nil
	// on a JSON-Object-Mode finalization turn, emitted as
	// response_format:{type:json_object}; nil on ordinary turns so the field is
	// omitted and the request head stays byte-stable.
	ResponseFormat *jsonObjectMode `json:"response_format,omitempty"`
}

// thinkingEnabler is DeepSeek's thinking-mode toggle; the enabled form keeps
// thinking default-on for agent loops (docs/spec.md §6).
type thinkingEnabler struct {
	Type string `json:"type"`
}

// thinkingControl returns the enabled thinking toggle when req opts in, else
// nil so the field is omitted.
func thinkingControl(req Request) *thinkingEnabler {
	if !req.ThinkingEnabled {
		return nil
	}
	return &thinkingEnabler{Type: "enabled"}
}

// jsonObjectMode is OpenAI's constrained-output response_format; its enabled
// form asks the provider to return a valid JSON object (issue #59).
type jsonObjectMode struct {
	Type string `json:"type"`
}

// SupportedGenerationControls declares that this Chat-Completions client can
// honor the Generation Budget and JSON Object Mode controls (it wire-emits
// max_completion_tokens and response_format on special turns, docs/spec.md §13 /
// issues #59–#60). Higher layers consult this via NegotiateGenerationControls;
// the other two generation controls are not supported here.
func (o *OpenAICompatible) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return []GenerationControl{GenerationControlGenerationBudget, GenerationControlJSONObjectMode}, nil
}

// maxOutputTokens returns the Generation Budget for req as an int usable as a
// wire max_completion_tokens: it is 0 when no budget was requested so the field
// is omitted. A negative value (invalid) is clamped to 0 rather than emitted, so
// an internal caller can never accidentally send a nonsensical cap.
func maxOutputTokens(req Request) int {
	if req.MaxOutputTokens <= 0 {
		return 0
	}
	return req.MaxOutputTokens
}

// jsonObjectModeControl returns the JSON Object Mode response_format for req
// when the caller opted into it, else nil so the field is omitted. Only a JSON
// Object Mode finalization special turn sets it (issue #59).
func jsonObjectModeControl(req Request) *jsonObjectMode {
	if !req.JSONObjectMode {
		return nil
	}
	return &jsonObjectMode{Type: "json_object"}
}

// promptCacheKey returns the session-scoped prompt cache key for req when the
// caller opted into deepseek's session cache (docs/spec.md §4), else empty so
// the field is omitted from the body. When unset the gateway may still cache by
// request prefix, but Eitri explicitly opts in so the cache namespace is the
// session id.
func promptCacheKey(req Request) string {
	if req.SetCacheKey {
		return req.SessionKey
	}
	return ""
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// HTTPError reports a non-2xx provider response.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d", e.Code)
}

// openAIStream adapts parsed SSE events into the Stream seam, mapping [DONE]
// to a Done chunk and io.EOF to io.EOF, accumulating tool_call fragments.
type openAIStream struct {
	ev  *sse
	acc *toolAccumulator
}

// Next implements Stream.
func (os *openAIStream) Next() (Chunk, error) {
	e, err := os.ev.Next()
	if errors.Is(err, io.EOF) {
		return Chunk{}, io.EOF
	}
	if err != nil {
		return Chunk{}, err
	}
	return parseEvent(e.data, os.acc)
}
