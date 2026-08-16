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
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// sseFixture loads the committed Chat-Completions SSE fixture bytes for a
// stream-round-trip assertion.
func sseFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// copilotServer serves a Chat-Completions SSE stream and records the bearer
// token it observed on each request.
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

// TestCopilotStreamsWithValidStoredToken is the baseline Copilot batch turn: a
// valid stored access token is used directly as the bearer on the shared
// Chat-Completions wire, and the reasoning/answer stream matches the primary
// provider's, since Copilot re-expresses through the same engine seam —
// provider-agnostic dialect routing.
func TestCopilotStreamsWithValidStoredToken(t *testing.T) {
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

// TestCopilotBatchRefreshesExpiredToken is acceptance criterion (a): when the
// stored access token is expired/absent but a refresh token exists, batch renews
// non-interactively (never shows a device flow) and the run proceeds on the
// renewed token.
func TestCopilotBatchRefreshesExpiredToken(t *testing.T) {
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

// TestCopilotBatchNoTokenErrorsReauth is acceptance criterion (b): with neither
// a valid stored token nor a usable refresh path, batch fails cleanly with a
// re-auth-in-TUI message and never attempts an interactive device flow (the
// httptest server would record a request if one was wrongly made).
func TestCopilotBatchNoTokenErrorsReauth(t *testing.T) {
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

// TestCopilotWorksAfterTUIReAuth is acceptance criterion (c): after a TUI-side
// device-flow re-auth persists a fresh token to config, a batch run built from
// that refreshed config proceeds normally.
func TestCopilotWorksAfterTUIReAuth(t *testing.T) {
	srv, lastToken := copilotServer(t, sseFixture(t))
	// The persisted config holds the freshly device-flow'd token.
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

// drainOne consumes a Stream to its Done chunk, discarding the content.
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

// TestCopilotDeclaresGenerationControlCapabilities verifies the Copilot
// provider advertises the generation_budget and thinking_suppression controls
// through the generation-control capability surface, so higher layers can
// pre-flight a special turn's requirements before any wire
// call.
func TestCopilotDeclaresGenerationControlCapabilities(t *testing.T) {
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

// TestCopilotCapabilityMatchesWireBehavior ties the advertised thinking-
// suppression control to the wire shape that honors it: negotiation honors a
// required thinking_suppression request, and a thinking-off stream carries
// the explicit thinking:{type:disabled} suppression.
// TestCopilotDropsEffortWhenThinkingDisabled pins the wire shape
// alone; this test asserts advertisement and wire agree.
func TestCopilotCapabilityMatchesWireBehavior(t *testing.T) {
	cp := NewCopilot(config.CopilotConfig{AccessToken: "x"}, "http://example.invalid/chat/completions", nil, nil, nil)
	assertSuppressionHonored(t, cp)
	streamAssertSuppression(t, func(url string) Provider {
		return NewCopilot(config.CopilotConfig{AccessToken: "x"}, url, nil, nil, nil)
	}, "github-copilot")
}

// TestCopilotDropsEffortWhenThinkingDisabled verifies the non-thinking wire
// guarantee also holds on the Copilot provider:
// when the caller disables thinking, `reasoning_effort` is dropped from the
// request body even if a non-empty effort is retained. Unlike the primary
// provider (which omits the toggle entirely), the Copilot backend follows its
// reasoning-on server default unless told otherwise, so the request carries an
// explicit `thinking:{type:disabled}` suppression instead.
func TestCopilotDropsEffortWhenThinkingDisabled(t *testing.T) {
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

// TestCopilotSendsThinkingEnabledWhenThinkingOn verifies the thinking-enabled
// wire shape also holds on the Copilot provider: when the caller
// keeps thinking on, the request carries an explicit `thinking:{type:enabled}`
// toggle plus the normalized reasoning_effort.
func TestCopilotSendsThinkingEnabledWhenThinkingOn(t *testing.T) {
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
