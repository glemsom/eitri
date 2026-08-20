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

func TestFromConfigRoutesOpenCodeGo(t *testing.T) {
	t.Parallel()
	p, err := FromConfig(config.Config{Provider: "opencode-go", Model: "deepseek-v4-flash"}, ProviderEnv{OpenCodeKey: "k"})
	if err != nil {
		t.Fatalf("FromConfig() error = %v, want nil", err)
	}
	if _, ok := p.(*OpenAICompatible); !ok {
		t.Fatalf("FromConfig(opencode-go) = %T, want *OpenAICompatible", p)
	}
}

func TestFromConfigRoutesCustomOpenAI(t *testing.T) {
	t.Parallel()
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

func TestFromConfigCustomOpenAIMissingSettingsFails(t *testing.T) {
	t.Parallel()
	_, err := FromConfig(config.Config{Provider: "custom-openai"}, ProviderEnv{})
	if err == nil {
		t.Fatalf("FromConfig(custom-openai) with no endpoint = nil error, want an error")
	}
}

func TestFromConfigRoutesCopilot(t *testing.T) {
	t.Parallel()
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

func TestFromConfigThinkingSuppressionMatchesWireBehavior(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		cfg         config.Config
		env         ProviderEnv
		wireThrough func(t *testing.T, url string) Provider // nil: stream a sibling
	}{
		{
			name: "opencode-go",
			cfg:  config.Config{Provider: ProviderOpenCodeGo},
			env:  ProviderEnv{OpenCodeKey: "k"},
			wireThrough: func(t *testing.T, url string) Provider {
				t.Helper()
				p, err := FromConfig(config.Config{Provider: ProviderOpenCodeGo}, ProviderEnv{OpenCodeKey: "k", OpenCodeURL: url})
				if err != nil {
					t.Fatalf("FromConfig() error = %v, want nil", err)
				}
				return p
			},
		},
		{
			name: "custom-openai",
			cfg: config.Config{Provider: ProviderCustomOpenAI,
				CustomOpenAI: config.OpenAIConfig{BaseURL: "http://example.invalid/v1/chat/completions", Key: "k"}},
			wireThrough: func(t *testing.T, url string) Provider {
				t.Helper()
				p, err := FromConfig(config.Config{Provider: ProviderCustomOpenAI,
					CustomOpenAI: config.OpenAIConfig{BaseURL: url, Key: "k"}}, ProviderEnv{})
				if err != nil {
					t.Fatalf("FromConfig() error = %v, want nil", err)
				}
				return p
			},
		},
		{
			name:        "github-copilot",
			cfg:         config.Config{Provider: ProviderCopilot, Copilot: config.CopilotConfig{AccessToken: "x"}},
			env:         ProviderEnv{},
			wireThrough: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := FromConfig(tc.cfg, tc.env)
			if err != nil {
				t.Fatalf("FromConfig() error = %v, want nil", err)
			}
			assertSuppressionHonored(t, p)

			streamAssertSuppression(t, func(url string) Provider {
				if tc.wireThrough != nil {
					return tc.wireThrough(t, url)
				}
				return NewCopilot(config.CopilotConfig{AccessToken: "x"}, url, nil, nil, nil)
			}, tc.name)
		})
	}
}

func streamAssertSuppression(t *testing.T, build func(url string) Provider, family string) {
	t.Helper()
	var sawThinking bool
	var disabled bool
	srv := thinkingWireServer(t, func(parsed map[string]any) {
		sawThinking = parsed["thinking"] != nil
		if th, ok := parsed["thinking"].(map[string]any); ok {
			disabled = th["type"] == "disabled"
		}
	})
	defer srv.Close()

	p := build(srv.URL)
	if _, err := p.Stream(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	switch family {
	case "opencode-go", "custom-openai":
		if sawThinking {
			t.Error("thinking-off stream carried the thinking toggle, want omitted")
		}
	case "github-copilot":
		if !disabled {
			t.Error("thinking-off stream lacked explicit {type:disabled} suppression")
		}
	}
}

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

func TestFromConfigUnknownProviderFails(t *testing.T) {
	t.Parallel()
	_, err := FromConfig(config.Config{Provider: "nope"}, ProviderEnv{})
	if err == nil {
		t.Fatalf("FromConfig(unknown) = nil error, want an error")
	}
}
