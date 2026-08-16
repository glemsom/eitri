package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// TestFromConfigRoutesOpenCodeGo verifies the saved opencode-go provider builds
// the OpenAI-compatible primary client.
func TestFromConfigRoutesOpenCodeGo(t *testing.T) {
	p, err := FromConfig(config.Config{Provider: "opencode-go", Model: "deepseek-v4-flash"}, ProviderEnv{OpenCodeKey: "k"})
	if err != nil {
		t.Fatalf("FromConfig() error = %v, want nil", err)
	}
	if _, ok := p.(*OpenAICompatible); !ok {
		t.Fatalf("FromConfig(opencode-go) = %T, want *OpenAICompatible", p)
	}
}

// TestFromConfigRoutesCustomOpenAI verifies a saved custom-openai provider builds
// an OpenAI-compatible client against the user-supplied base URL and key,
// routed through the same Chat-Completions dialect.
func TestFromConfigRoutesCustomOpenAI(t *testing.T) {
	cfg := config.Config{
		Provider: "custom-openai",
		Model:    "my-model",
		CustomOpenAI: config.OpenAIConfig{
			BaseURL: "https://my.endpoint/v1/chat/completions",
			Key:     "my-key",
		},
	}
	p, err := FromConfig(cfg, ProviderEnv{})
	if err != nil {
		t.Fatalf("FromConfig() error = %v, want nil", err)
	}
	oc, ok := p.(*OpenAICompatible)
	if !ok {
		t.Fatalf("FromConfig(custom-openai) = %T, want *OpenAICompatible", p)
	}
	if oc.url != "https://my.endpoint/v1/chat/completions" || oc.apiKey != "my-key" {
		t.Fatalf("custom-openai client = {url:%q apiKey:%q}, want the configured endpoint+key", oc.url, oc.apiKey)
	}
}

// TestFromConfigCustomOpenAIMissingSettingsFails verifies a custom-openai
// selection without a configured endpoint fails cleanly at build time rather
// than silently talking nowhere.
func TestFromConfigCustomOpenAIMissingSettingsFails(t *testing.T) {
	_, err := FromConfig(config.Config{Provider: "custom-openai"}, ProviderEnv{})
	if err == nil {
		t.Fatalf("FromConfig(custom-openai) with no endpoint = nil error, want an error")
	}
}

// TestFromConfigRoutesCopilot verifies a saved github-copilot provider builds a
// Copilot provider carrying the persisted device-flow credential.
func TestFromConfigRoutesCopilot(t *testing.T) {
	cfg := config.Config{
		Provider: "github-copilot",
		Model:    "gpt-4o",
		Copilot:  config.CopilotConfig{AccessToken: "acc", RefreshToken: "ref"},
	}
	p, err := FromConfig(cfg, ProviderEnv{})
	if err != nil {
		t.Fatalf("FromConfig() error = %v, want nil", err)
	}
	cp, ok := p.(*CopilotProvider)
	if !ok {
		t.Fatalf("FromConfig(github-copilot) = %T, want *CopilotProvider", p)
	}
	tok, err := cp.bearer(context.Background())
	if err != nil {
		t.Fatalf("bearer() error = %v, want the stored token", err)
	}
	if tok != "acc" {
		t.Fatalf("bearer() = %q, want the persisted access token", tok)
	}
}

// TestFromConfigThinkingSuppressionMatchesWireBehavior asserts each provider
// family's advertised thinking-suppression capability matches its actual wire
// shape (issue #265 AC-4): negotiations against the built provider honor a
// required thinking_suppression request, and a stream through the same provider
// emits the family's suppression form — omission of the thinking toggle on the
// openai-compatible path (issue #54), an explicit thinking:{type:disabled} on
// the copilot path (issue #263). Deterministic httptest servers, no network.
func TestFromConfigThinkingSuppressionMatchesWireBehavior(t *testing.T) {
	cases := []struct {
		name       string
		cfg        config.Config
		env        ProviderEnv
		wireChecks func(t *testing.T, p Provider)
	}{
		{
			name: "opencode-go",
			cfg:  config.Config{Provider: ProviderOpenCodeGo},
			env:  ProviderEnv{OpenCodeKey: "k"},
			wireChecks: func(t *testing.T, p Provider) {
				assertSuppressionHonored(t, p)
				var sawThinking bool
				srv := thinkingWireServer(t, func(parsed map[string]any) {
					sawThinking = parsed["thinking"] != nil
				})
				defer srv.Close()
				oc := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
				if _, err := oc.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
					t.Fatalf("Stream() error = %v, want nil", err)
				}
				if sawThinking {
					t.Error("thinking-off stream carried the thinking toggle, want omitted (issue #54)")
				}
			},
		},
		{
			name: "custom-openai",
			cfg: config.Config{Provider: ProviderCustomOpenAI,
				CustomOpenAI: config.OpenAIConfig{BaseURL: "http://example.invalid/v1/chat/completions", Key: "k"}},
			wireChecks: func(t *testing.T, p Provider) {
				assertSuppressionHonored(t, p)
				var sawThinking bool
				srv := thinkingWireServer(t, func(parsed map[string]any) {
					sawThinking = parsed["thinking"] != nil
				})
				defer srv.Close()
				oc := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
				if _, err := oc.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
					t.Fatalf("Stream() error = %v, want nil", err)
				}
				if sawThinking {
					t.Error("thinking-off stream carried the thinking toggle, want omitted (issue #54)")
				}
			},
		},
		{
			name: "github-copilot",
			cfg:  config.Config{Provider: ProviderCopilot, Copilot: config.CopilotConfig{AccessToken: "x"}},
			env:  ProviderEnv{},
			wireChecks: func(t *testing.T, p Provider) {
				assertSuppressionHonored(t, p)
				var disabled bool
				srv := thinkingWireServer(t, func(parsed map[string]any) {
					if th, ok := parsed["thinking"].(map[string]any); ok {
						disabled = th["type"] == "disabled"
					}
				})
				defer srv.Close()
				cp := NewCopilot(config.CopilotConfig{AccessToken: "x"}, srv.URL+"/chat/completions", srv.Client(), nil, nil)
				if _, err := cp.Stream(context.Background(), Request{Model: "m"}); err != nil {
					t.Fatalf("Stream() error = %v, want nil", err)
				}
				if !disabled {
					t.Error("thinking-off stream lacked explicit {type:disabled} suppression (issue #263)")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := FromConfig(tc.cfg, tc.env)
			if err != nil {
				t.Fatalf("FromConfig() error = %v, want nil", err)
			}
			tc.wireChecks(t, p)
		})
	}
}

// assertSuppressionHonored negotiates a required thinking-suppression request
// against p and fails unless it is honored.
func assertSuppressionHonored(t *testing.T, p Provider) {
	t.Helper()
	honored, err := NegotiateGenerationControls(context.Background(), p, []ControlRequirement{
		{Control: GenerationControlThinkingSuppression, Required: true},
	})
	if err != nil {
		t.Fatalf("NegotiateGenerationControls() error = %v, want nil (honored)", err)
	}
	if !sameControls(honored, []string{string(GenerationControlThinkingSuppression)}) {
		t.Fatalf("NegotiateGenerationControls() = %v, want [%s]", honored, GenerationControlThinkingSuppression)
	}
}

// thinkingWireServer serves an SSE fixture and reports each request's parsed
// body to inspect.
func thinkingWireServer(t *testing.T, inspect func(map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Errorf("request body not JSON: %v", err)
		}
		inspect(parsed)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fixture, _ := os.ReadFile("testdata/usage-final.sse")
		_, _ = w.Write(fixture)
	}))
	return srv
}

// TestFromConfigUnknownProviderFails verifies an unsupported provider selection
// is rejected explicitly rather than silently defaulting.
func TestFromConfigUnknownProviderFails(t *testing.T) {
	_, err := FromConfig(config.Config{Provider: "nope"}, ProviderEnv{})
	if err == nil {
		t.Fatalf("FromConfig(unknown) = nil error, want an error")
	}
}
