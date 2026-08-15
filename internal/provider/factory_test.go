package provider

import (
	"context"
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

// TestFromConfigUnknownProviderFails verifies an unsupported provider selection
// is rejected explicitly rather than silently defaulting.
func TestFromConfigUnknownProviderFails(t *testing.T) {
	_, err := FromConfig(config.Config{Provider: "nope"}, ProviderEnv{})
	if err == nil {
		t.Fatalf("FromConfig(unknown) = nil error, want an error")
	}
}
