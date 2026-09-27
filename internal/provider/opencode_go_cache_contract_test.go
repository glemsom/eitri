package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type openCodeGoDialect struct {
	name, modelEnv, path string
}

var openCodeGoDialects = []openCodeGoDialect{
	{name: "chat", modelEnv: "EITRI_OPENCODE_GO_CHAT_MODEL", path: "/v1/chat/completions"},
	{name: "messages", modelEnv: "EITRI_OPENCODE_GO_MESSAGES_MODEL", path: "/v1/messages"},
}

type openCodeGoPromptCacheTurn struct {
	SessionKey      string
	Answer          string
	Method, Path    string
	Body            string
	IdentityHeaders http.Header
	Usage           *Usage
}

type openCodeGoPromptCacheObservation struct {
	Cold, Repeated openCodeGoPromptCacheTurn
}

func TestOpenCodeGoPromptCacheContract(t *testing.T) {
	for _, dialect := range openCodeGoDialects {
		t.Run(dialect.name, func(t *testing.T) {
			observation := runOpenCodeGoPromptCacheContract(t, dialect)
			assertOpenCodeGoPromptCacheTurn(t, dialect, "cold", observation.Cold)
			assertOpenCodeGoPromptCacheTurn(t, dialect, "repeated", observation.Repeated)
			if observation.Cold.SessionKey != observation.Repeated.SessionKey {
				t.Fatalf("SessionKey changed between cold and repeated turns: %q != %q", observation.Cold.SessionKey, observation.Repeated.SessionKey)
			}
			if got, want := observation.Cold.IdentityHeaders.Get("X-Opencode-Session"), observation.Repeated.IdentityHeaders.Get("X-Opencode-Session"); got != want {
				t.Fatalf("X-Opencode-Session changed between cold and repeated turns: %q != %q", got, want)
			}
			logOpenCodeGoPromptCacheTurn(t, "cold", observation.Cold)
			logOpenCodeGoPromptCacheTurn(t, "repeated", observation.Repeated)
		})
	}
}

func runOpenCodeGoPromptCacheContract(t *testing.T, dialect openCodeGoDialect) openCodeGoPromptCacheObservation {
	t.Helper()
	apiKey, model := os.Getenv("EITRI_OPENCODE_GO_API_KEY"), os.Getenv(dialect.modelEnv)
	if apiKey == "" || model == "" {
		t.Skipf("set EITRI_OPENCODE_GO_API_KEY and %s to verify the %s contract", dialect.modelEnv, dialect.name)
	}
	endpoint := os.Getenv("EITRI_OPENCODE_GO_URL")
	if endpoint == "" {
		endpoint = "https://opencode.ai/zen/go/v1/chat/completions"
	}
	recorder := &openCodeGoRequestRecorder{next: http.DefaultTransport}
	client := NewOpenCodeGo(apiKey, endpoint)
	client.http = &http.Client{Transport: recorder}
	sessionKey := fmt.Sprintf("eitri-prompt-cache-contract-%d", time.Now().UnixNano())
	coldRequest := Request{
		Model: model, ProviderID: ProviderOpenCodeGo, SetCacheKey: true, SessionKey: sessionKey,
		Messages: []Message{{Role: RoleSystem, Content: "Reply with exactly one word."}, {Role: RoleUser, Content: "cold"}},
	}
	cold := streamOpenCodeGoPromptCacheTurn(t, client, recorder, coldRequest)
	repeatedRequest := coldRequest
	repeatedRequest.Messages = append(repeatedRequest.Messages, Message{Role: RoleAssistant, Content: cold.Answer}, Message{Role: RoleUser, Content: "repeated"})
	repeated := streamOpenCodeGoPromptCacheTurn(t, client, recorder, repeatedRequest)
	return openCodeGoPromptCacheObservation{Cold: cold, Repeated: repeated}
}

func streamOpenCodeGoPromptCacheTurn(t *testing.T, client *OpenAICompatible, recorder *openCodeGoRequestRecorder, req Request) openCodeGoPromptCacheTurn {
	t.Helper()
	stream, err := client.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	answer, usage, err := consume(stream)
	if err != nil {
		t.Fatalf("consume() error: %v", err)
	}
	outbound := recorder.last()
	outbound.SessionKey, outbound.Answer, outbound.Usage = req.SessionKey, answer, usage
	if outbound.Method == "" {
		t.Fatal("Stream() made no outbound request")
	}
	return outbound
}

func assertOpenCodeGoPromptCacheTurn(t *testing.T, dialect openCodeGoDialect, name string, turn openCodeGoPromptCacheTurn) {
	t.Helper()
	if turn.Method != http.MethodPost {
		t.Errorf("%s method = %q, want %q", name, turn.Method, http.MethodPost)
	}
	if turn.Path != dialect.path {
		t.Errorf("%s path = %q, want %q", name, turn.Path, dialect.path)
	}
	if got := turn.IdentityHeaders.Get("X-Opencode-Session"); got != turn.SessionKey {
		t.Errorf("%s X-Opencode-Session = %q, want %q", name, got, turn.SessionKey)
	}
	var body any
	if err := json.Unmarshal([]byte(turn.Body), &body); err != nil {
		t.Errorf("%s request body is not JSON: %v", name, err)
	} else {
		for _, extension := range []string{"prompt_cache_key", "prompt_cache_retention", "cache_control"} {
			if jsonContainsKey(body, extension) {
				t.Errorf("%s request emitted unverified %s extension: %s", name, extension, turn.Body)
			}
		}
	}
	if turn.Usage == nil {
		t.Errorf("%s provider omitted terminal streamed usage", name)
		return
	}
	if turn.Usage.PromptTokens < 0 || turn.Usage.CompletionTokens < 0 || turn.Usage.PromptCacheHitTokens < 0 || turn.Usage.PromptCacheMissTokens < 0 || turn.Usage.PromptCacheWriteTokens < 0 {
		t.Errorf("%s terminal usage contains negative tokens: %+v", name, *turn.Usage)
	}
}

func jsonContainsKey(value any, key string) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value[key]; ok {
			return true
		}
		for _, child := range value {
			if jsonContainsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if jsonContainsKey(child, key) {
				return true
			}
		}
	}
	return false
}

func logOpenCodeGoPromptCacheTurn(t *testing.T, name string, turn openCodeGoPromptCacheTurn) {
	t.Helper()
	t.Logf("%s outbound request: %s %s headers=%v body=%s", name, turn.Method, turn.Path, turn.IdentityHeaders, turn.Body)
	t.Logf("%s final usage: %+v", name, *turn.Usage)
}

type openCodeGoRequestRecorder struct {
	next http.RoundTripper
	mu   sync.Mutex
	turn openCodeGoPromptCacheTurn
}

func (r *openCodeGoRequestRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	r.mu.Lock()
	r.turn = openCodeGoPromptCacheTurn{Method: req.Method, Path: requestPath(req.URL), Body: string(body), IdentityHeaders: recordedIdentityHeaders(req.Header)}
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func (r *openCodeGoRequestRecorder) last() openCodeGoPromptCacheTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turn
}

func requestPath(u *url.URL) string {
	if u.RawQuery == "" {
		return u.EscapedPath()
	}
	return u.EscapedPath() + "?" + u.RawQuery
}

func recordedIdentityHeaders(headers http.Header) http.Header {
	out := make(http.Header)
	for _, name := range []string{"Authorization", "X-Api-Key", "X-Opencode-Session", "Anthropic-Version", "User-Agent"} {
		if value := headers.Get(name); value != "" {
			if name == "Authorization" || name == "X-Api-Key" {
				value = redactCredential(value)
			}
			out.Set(name, value)
		}
	}
	return out
}

func redactCredential(value string) string {
	if scheme, _, ok := strings.Cut(value, " "); ok {
		return scheme + " [REDACTED]"
	}
	return "[REDACTED]"
}

func TestOpenCodeGoPromptCacheTurnRecordsOutboundRequestAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	recorder := &openCodeGoRequestRecorder{next: server.Client().Transport}
	client := NewOpenCodeGo("test-key", server.URL+"/v1/chat/completions")
	client.http = &http.Client{Transport: recorder}
	turn := streamOpenCodeGoPromptCacheTurn(t, client, recorder, Request{Model: "deepseek-v4-flash", SessionKey: "session-1", Messages: []Message{{Role: RoleUser, Content: "cold"}}})
	if turn.Method != http.MethodPost || turn.Path != "/v1/chat/completions" || !strings.Contains(turn.Body, `"model":"deepseek-v4-flash"`) {
		t.Errorf("outbound request = %s %s %s", turn.Method, turn.Path, turn.Body)
	}
	if got := turn.IdentityHeaders.Get("X-Opencode-Session"); got != "session-1" {
		t.Errorf("X-Opencode-Session = %q, want session-1", got)
	}
	if got := turn.IdentityHeaders.Get("Authorization"); got != "Bearer [REDACTED]" {
		t.Errorf("Authorization = %q, want redacted bearer credential", got)
	}
	if turn.Usage == nil || turn.Usage.PromptTokens != 3 || turn.Usage.CompletionTokens != 1 {
		t.Errorf("usage = %+v, want prompt=3 completion=1", turn.Usage)
	}
}
