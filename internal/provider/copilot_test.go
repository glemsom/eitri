package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

func sseFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func copilotServer(t *testing.T, body []byte) (*httptest.Server, func() string) {
	t.Helper()
	var mu tokenMu
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.add(r.Header.Get("Authorization"))
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, mu.get
}

type tokenMu struct {
	mu  int
	tok string
}

func (m *tokenMu) add(bearer string) { m.mu++; m.tok = bearer }
func (m *tokenMu) get() string       { return m.tok }

func drainAll(s Stream) (content, reasoning string, last Chunk, err error) {
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return content, reasoning, last, nil
		}
		if err != nil {
			return content, reasoning, last, err
		}
		content += c.Content
		reasoning += c.ReasoningContent
		last = c
		if c.Done {
			return content, reasoning, last, nil
		}
	}
}

func TestCopilotStreamsWithValidStoredToken(t *testing.T) {
	t.Parallel()
	srv, lastToken := copilotServer(t, sseFixture(t))
	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(),
		nil, nil)

	s, err := cp.Stream(context.Background(), Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	var content, reasoning string
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		content += c.Content
		reasoning += c.ReasoningContent
		if c.Done {
			break
		}
	}
	if content != "Hello world" || reasoning != "think step by step" {
		t.Fatalf("content=%q reasoning=%q, want Hello world / think step by step", content, reasoning)
	}
	if lastToken() != "Bearer stored-access" {
		t.Fatalf("request Authorization = %q, want Bearer stored-access", lastToken())
	}
}

func TestCopilotStreamSendsIntegrationIdentityHeaders(t *testing.T) {
	t.Parallel()
	var integrationID, editorVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		integrationID = r.Header.Get("Copilot-Integration-Id")
		editorVersion = r.Header.Get("Editor-Version")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sseFixture(t))
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	if _, err := cp.Stream(context.Background(), Request{Model: "gpt-4o"}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if integrationID != copilotIntegrationID {
		t.Errorf("Copilot-Integration-Id = %q, want %q", integrationID, copilotIntegrationID)
	}
	if editorVersion == "" {
		t.Error("Editor-Version header missing, want non-empty")
	}
}

func TestCopilotBearerConcurrentRefreshIsRaceFree(t *testing.T) {
	t.Parallel()
	refresh := func(_ context.Context, refreshToken string) (config.CopilotConfig, error) {
		if refreshToken != "the-refresh" {
			return config.CopilotConfig{}, errors.New("wrong refresh token")
		}
		return config.CopilotConfig{
			AccessToken:  "renewed-access",
			RefreshToken: "the-refresh",
			ExpiresAt:    time.Now().Unix() + 3600,
		}, nil
	}
	cp := NewCopilot(config.CopilotConfig{RefreshToken: "the-refresh"}, "http://example.invalid/chat/completions", nil, refresh, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := cp.bearer(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if tok != "renewed-access" {
				errs <- errors.New("unexpected token " + tok)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopilotBatchRefreshesExpiredToken(t *testing.T) {
	t.Parallel()
	srv, lastToken := copilotServer(t, sseFixture(t))
	var persisted *config.CopilotConfig
	refresh := func(_ context.Context, refreshToken string) (config.CopilotConfig, error) {
		if refreshToken != "the-refresh" {
			t.Fatalf("refresh() got refresh token %q, want the-refresh", refreshToken)
		}
		return config.CopilotConfig{
			AccessToken:  "renewed-access",
			RefreshToken: "the-refresh",
			ExpiresAt:    time.Now().Unix() + 3600,
		}, nil
	}
	cp := NewCopilot(config.CopilotConfig{RefreshToken: "the-refresh"}, srv.URL+"/chat/completions", srv.Client(),
		refresh, func(c config.CopilotConfig) error { persisted = &c; return nil })

	s, err := cp.Stream(context.Background(), Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if _, err := drainOne(s); err != nil {
		t.Fatalf("streaming turn: %v", err)
	}
	if lastToken() != "Bearer renewed-access" {
		t.Fatalf("request Authorization = %q, want Bearer renewed-access", lastToken())
	}
	if persisted == nil || persisted.AccessToken != "renewed-access" {
		t.Fatalf("persist() = %+v, want renewed access token saved to config", persisted)
	}
}

func TestCopilotBatchNoTokenErrorsReauth(t *testing.T) {
	t.Parallel()
	reqs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqs++
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	_, err := cp.Stream(context.Background(), Request{Model: "gpt-4o"})
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("Stream() error = %v, want ErrReauthRequired", err)
	}
	if !strings.Contains(err.Error(), "TUI") {
		t.Fatalf("error %q does not direct the user to re-auth in the TUI", err)
	}
	if reqs != 0 {
		t.Fatalf("chat endpoint hit %d times; batch must never run the device flow", reqs)
	}
}

func TestCopilotWorksAfterTUIReAuth(t *testing.T) {
	t.Parallel()
	srv, lastToken := copilotServer(t, sseFixture(t))
	cp := NewCopilot(config.CopilotConfig{
		AccessToken:  "fresh-from-tui",
		RefreshToken: "fresh-refresh",
		ExpiresAt:    time.Now().Unix() + 3600,
	}, srv.URL+"/chat/completions", srv.Client(), nil, nil)

	s, err := cp.Stream(context.Background(), Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if _, err := drainOne(s); err != nil {
		t.Fatalf("streaming turn: %v", err)
	}
	if lastToken() != "Bearer fresh-from-tui" {
		t.Fatalf("request Authorization = %q, want Bearer fresh-from-tui", lastToken())
	}
}

func TestCopilotDiscoversResponsesOnlyModelEndpoint(t *testing.T) {
	t.Parallel()
	chatReqs := 0
	responsesReqs := 0
	modelsReqs := 0
	var modelsIntegrationID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelsReqs++
			modelsIntegrationID = r.Header.Get("Copilot-Integration-Id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.4-mini","endpoints":["responses"]}]}`))
		case "/chat/completions":
			chatReqs++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model \"gpt-5.4-mini\" is not accessible via the /chat/completions endpoint","code":"unsupported_api_for_model"}}`))
		case "/responses":
			responsesReqs++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello world\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4-mini\",\"created_at\":1,\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0}},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello world\"}]}]}}\n\n")
		default:
			t.Fatalf("path = %s, want /models or /responses", r.URL.Path)
		}
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	models, err := cp.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v, want nil", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.4-mini" {
		t.Fatalf("Models() = %v, want one gpt-5.4-mini model", models)
	}
	if models[0].EndpointKind != EndpointResponses {
		t.Fatalf("Models()[0].EndpointKind = %q, want %q", models[0].EndpointKind, EndpointResponses)
	}

	s, err := cp.Stream(context.Background(), Request{Model: "gpt-5.4-mini", Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if _, err := drainOne(s); err != nil {
		t.Fatalf("responses stream: %v", err)
	}
	if modelsReqs != 1 || chatReqs != 0 || responsesReqs != 1 {
		t.Fatalf("path counts models=%d chat=%d responses=%d, want 1/0/1", modelsReqs, chatReqs, responsesReqs)
	}
	if modelsIntegrationID != copilotIntegrationID {
		t.Errorf("models request Copilot-Integration-Id = %q, want %q", modelsIntegrationID, copilotIntegrationID)
	}
}

func TestCopilotRetriesResponsesForResponsesOnlyModel(t *testing.T) {
	t.Parallel()
	chatReqs := 0
	responsesReqs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			chatReqs++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model \"gpt-5.4-mini\" is not accessible via the /chat/completions endpoint","code":"unsupported_api_for_model"}}`))
		case "/responses":
			responsesReqs++
			defer r.Body.Close()
			body, _ := io.ReadAll(r.Body)
			var parsed map[string]any
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("responses body not JSON: %v", err)
			}
			if parsed["messages"] != nil {
				t.Fatalf("responses body leaked chat-completions messages field: %s", body)
			}
			input, ok := parsed["input"].([]any)
			if !ok || len(input) != 1 {
				t.Fatalf("responses input = %#v, want one user item", parsed["input"])
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4-mini\",\"created_at\":1,\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0}},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello world\"}]}]}}\n\n")
		default:
			t.Fatalf("path = %s, want /chat/completions or /responses", r.URL.Path)
		}
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	req := Request{Model: "gpt-5.4-mini", Messages: []Message{{Role: RoleUser, Content: "hello"}}}

	s, err := cp.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("first Stream() error = %v, want nil", err)
	}
	content, _, last, err := drainAll(s)
	if err != nil {
		t.Fatalf("first responses stream: %v", err)
	}
	if content != "Hello world" {
		t.Fatalf("first content = %q, want Hello world", content)
	}
	if !last.Done || last.Usage == nil || last.Usage.PromptTokens != 7 || last.Usage.CompletionTokens != 2 {
		t.Fatalf("first terminal chunk = %+v, want done chunk with usage 7/2", last)
	}
	if chatReqs != 1 || responsesReqs != 1 {
		t.Fatalf("first path counts chat=%d responses=%d, want 1/1", chatReqs, responsesReqs)
	}

	s, err = cp.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("second Stream() error = %v, want nil", err)
	}
	if _, err := drainOne(s); err != nil {
		t.Fatalf("second responses stream: %v", err)
	}
	if chatReqs != 1 || responsesReqs != 2 {
		t.Fatalf("cached path counts chat=%d responses=%d, want 1/2", chatReqs, responsesReqs)
	}
}

func TestCopilotResponsesStreamToolCalls(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model \"gpt-5.4-mini\" is not accessible via the /chat/completions endpoint","code":"unsupported_api_for_model"}}`))
		case "/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"path\\\":\\\"x\\\"}\"}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"x\\\"}\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_tool\",\"model\":\"gpt-5.4-mini\",\"created_at\":2,\"usage\":{\"input_tokens\":5,\"output_tokens\":1},\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"x\\\"}\"}]}}\n\n")
		default:
			t.Fatalf("path = %s, want /chat/completions or /responses", r.URL.Path)
		}
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	s, err := cp.Stream(context.Background(), Request{Model: "gpt-5.4-mini", Messages: []Message{{Role: RoleUser, Content: "use tool"}}})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	_, _, last, err := drainAll(s)
	if err != nil {
		t.Fatalf("responses tool-call stream: %v", err)
	}
	if !last.Done {
		t.Fatalf("terminal chunk Done = false, want true")
	}
	if last.FinishReason != "tool_calls" {
		t.Fatalf("terminal FinishReason = %q, want tool_calls", last.FinishReason)
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("terminal ToolCalls = %v, want one call", last.ToolCalls)
	}
	if got := last.ToolCalls[0]; got.ID != "call_1" || got.Name != "read" || got.Arguments != `{"path":"x"}` {
		t.Fatalf("terminal ToolCall = %+v, want call_1/read/{\"path\":\"x\"}", got)
	}
}

func TestCopilotChatDialectBuildExplicitThinkingToggle(t *testing.T) {
	t.Parallel()
	build := func(thinking bool) map[string]any {
		body, err := NewCopilotChatDialect().Build(Request{Model: "gpt-4o", ThinkingEnabled: thinking})
		if err != nil {
			t.Fatalf("Build() error = %v, want nil", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("body not JSON: %v", err)
		}
		return parsed
	}

	on := build(true)
	if th, ok := on["thinking"].(map[string]any); !ok || th["type"] != "enabled" {
		t.Errorf("thinking-on body = %#v, want thinking.type enabled", on)
	}
	if on["reasoning_effort"] != nil {
		t.Errorf("thinking-on body carried reasoning_effort without an effort, want omitted: %#v", on)
	}

	off := build(false)
	if th, ok := off["thinking"].(map[string]any); !ok || th["type"] != "disabled" {
		t.Errorf("thinking-off body = %#v, want thinking.type disabled", off)
	}
	if off["reasoning_effort"] != nil {
		t.Errorf("thinking-off body carried reasoning_effort, want omitted: %#v", off)
	}
}

func drainOne(s Stream) (string, error) {
	var content string
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return content, nil
		}
		if err != nil {
			return content, err
		}
		content += c.Content
		if c.Done {
			return content, nil
		}
	}
}

func TestCopilotDeclaresGenerationControlCapabilities(t *testing.T) {
	t.Parallel()
	cp := NewCopilot(config.CopilotConfig{AccessToken: "x"}, "http://example.invalid/chat/completions", nil, nil, nil)
	supp, err := cp.SupportedGenerationControls(context.Background())
	if err != nil {
		t.Fatalf("SupportedGenerationControls() error = %v, want nil", err)
	}
	want := []GenerationControl{GenerationControlGenerationBudget, GenerationControlThinkingSuppression}
	if len(supp) != len(want) {
		t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
	}
	for i := range want {
		if supp[i] != want[i] {
			t.Fatalf("SupportedGenerationControls() = %v, want %v", supp, want)
		}
	}
}

func TestCopilotRetriesResponsesOnReasoningEffortToolError(t *testing.T) {
	t.Parallel()
	modelsReqs := 0
	chatReqs := 0
	responsesReqs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelsReqs++
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.4-mini","endpoints":["responses"]}]}`))
		case "/chat/completions":
			chatReqs++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-5.4 in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.","code":"invalid_request_body"}}`))
		case "/responses":
			responsesReqs++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.4-mini\",\"created_at\":1,\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0}},\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello world\"}]}]}}\n\n")
		default:
			t.Fatalf("path = %s, want /models or /chat/completions or /responses", r.URL.Path)
		}
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	if _, err := cp.Stream(context.Background(), Request{Model: "gpt-5.4-mini", Messages: []Message{{Role: RoleUser, Content: "use tools"}}, Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "read"}}}, ThinkingEnabled: true, ReasoningEffort: "high"}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if chatReqs != 1 || responsesReqs != 1 {
		t.Fatalf("path counts models=%d chat=%d responses=%d, want 0/1/1", modelsReqs, chatReqs, responsesReqs)
	}
}

func TestCopilotDropsEffortWhenThinkingDisabled(t *testing.T) {
	t.Parallel()
	var sawEffort, thinkingDisabled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		sawEffort = parsed["reasoning_effort"] != nil
		if th, ok := parsed["thinking"].(map[string]any); ok {
			thinkingDisabled = th["type"] == "disabled"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sseFixture(t))
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "x"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	if _, err := cp.Stream(context.Background(), Request{
		Model:           "gpt-4o",
		ThinkingEnabled: false,
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if sawEffort {
		t.Error("request carried reasoning_effort, want omitted when thinking off")
	}
	if !thinkingDisabled {
		t.Error("request did not carry thinking suppression {type:disabled}, want present when thinking off")
	}
}

func TestCopilotSendsThinkingEnabledWhenThinkingOn(t *testing.T) {
	t.Parallel()
	var thinkingType string
	var sawEffort bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		if th, ok := parsed["thinking"].(map[string]any); ok {
			thinkingType, _ = th["type"].(string)
		}
		sawEffort = parsed["reasoning_effort"] != nil
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sseFixture(t))
	}))
	defer srv.Close()

	cp := NewCopilot(config.CopilotConfig{AccessToken: "x"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
	if _, err := cp.Stream(context.Background(), Request{
		Model:           "gpt-4o",
		ThinkingEnabled: true,
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	if thinkingType != "enabled" {
		t.Errorf("thinking type = %q, want enabled", thinkingType)
	}
	if !sawEffort {
		t.Error("request omitted reasoning_effort, want present when thinking on")
	}
}

func TestCopilotChatStreamsReasoningDelta(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning":"think from copilot"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"answer"}}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))
	srv, _ := copilotServer(t, body)
	cp := NewCopilot(config.CopilotConfig{AccessToken: "stored-access"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)

	s, err := cp.Stream(context.Background(), Request{Model: "gpt-5.5", ThinkingEnabled: true, ReasoningEffort: "low"})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	content, reasoning, _, err := drainAll(s)
	if err != nil {
		t.Fatalf("drainAll() error = %v, want nil", err)
	}
	if content != "answer" || reasoning != "think from copilot" {
		t.Fatalf("content=%q reasoning=%q, want answer / think from copilot", content, reasoning)
	}
}
