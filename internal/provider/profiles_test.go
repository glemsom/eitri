package provider_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestOpenAICompatibleProfilesBuildModelAndChatURLs(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"opencode_go", "custom_openai"} {
		prof, err := provider.Get(providerID)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", providerID, err)
		}

		if got := prof.ModelListURL("https://example.test/v1/"); got != "https://example.test/v1/models" {
			t.Errorf("%s ModelListURL = %q, want %q", providerID, got, "https://example.test/v1/models")
		}
		if got := prof.ChatCompletionsURL("https://example.test/v1/"); got != "https://example.test/v1/chat/completions" {
			t.Errorf("%s ChatCompletionsURL = %q, want %q", providerID, got, "https://example.test/v1/chat/completions")
		}
	}
}

func TestOpenAICompatibleProfilesParseOpenAIModelList(t *testing.T) {
	t.Parallel()

	prof, err := provider.Get("custom_openai")
	if err != nil {
		t.Fatal(err)
	}

	models, err := prof.ParseModelList(strings.NewReader(`{"object":"list","data":[{"id":"gpt-4"},{"id":""},{"id":"gpt-3.5-turbo"}]}`))
	if err != nil {
		t.Fatalf("ParseModelList error: %v", err)
	}

	want := []string{"gpt-4", "gpt-3.5-turbo"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, models[i], want[i])
		}
	}
}

func TestProfilesKeepExistingAPIKeyRequirements(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"opencode_go":    true,
		"custom_openai":  false,
		"github_copilot": true,
	}
	for providerID, wantRequired := range cases {
		prof, err := provider.Get(providerID)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", providerID, err)
		}
		if prof.APIKeyRequired != wantRequired {
			t.Errorf("%s APIKeyRequired = %v, want %v", providerID, prof.APIKeyRequired, wantRequired)
		}
	}
}

func TestGitHubCopilotProfileBuildsURLsAndHeaders(t *testing.T) {
	t.Parallel()

	prof, err := provider.Get("github_copilot")
	if err != nil {
		t.Fatal(err)
	}

	if prof.DisplayName != "GitHub Copilot" {
		t.Errorf("DisplayName = %q, want GitHub Copilot", prof.DisplayName)
	}
	if prof.DefaultBaseURL != "https://api.githubcopilot.com" {
		t.Errorf("DefaultBaseURL = %q, want https://api.githubcopilot.com", prof.DefaultBaseURL)
	}
	if got := prof.ModelListURL("https://api.githubcopilot.com/"); got != "https://api.githubcopilot.com/models" {
		t.Errorf("ModelListURL = %q, want https://api.githubcopilot.com/models", got)
	}
	if got := prof.ChatCompletionsURL("https://api.githubcopilot.com/"); got != "https://api.githubcopilot.com/chat/completions" {
		t.Errorf("ChatCompletionsURL = %q, want https://api.githubcopilot.com/chat/completions", got)
	}

	req := httptest.NewRequest("GET", prof.ModelListURL(prof.DefaultBaseURL), nil)
	prof.ApplyHeaders(req, "ghu-token")
	want := map[string]string{
		"Authorization":        "Bearer ghu-token",
		"User-Agent":           "Eitri",
		"X-GitHub-Api-Version": "2026-06-01",
		"Openai-Intent":        "conversation-panel",
		"x-initiator":          "user",
	}
	for name, value := range want {
		if got := req.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestDefaultGitHubCopilotOAuthConfigUsesBuiltInClientID(t *testing.T) {
	t.Setenv("EITRI_GITHUB_CLIENT_ID", "")

	cfg := provider.DefaultGitHubCopilotOAuthConfig(provider.GitHubCopilotOAuthConfig{})
	if cfg.ClientID == "" {
		t.Fatal("ClientID = empty, want built-in default")
	}
	if cfg.DeviceCodeURL != "https://github.com/login/device/code" {
		t.Fatalf("DeviceCodeURL = %q, want GitHub default", cfg.DeviceCodeURL)
	}
	if cfg.AccessTokenURL != "https://github.com/login/oauth/access_token" {
		t.Fatalf("AccessTokenURL = %q, want GitHub default", cfg.AccessTokenURL)
	}
	if cfg.Scope != "read:user" {
		t.Fatalf("Scope = %q, want read:user", cfg.Scope)
	}
}

func TestDefaultGitHubCopilotOAuthConfigAllowsEnvOverride(t *testing.T) {
	const want = "client-from-env"
	t.Setenv("EITRI_GITHUB_CLIENT_ID", want)

	cfg := provider.DefaultGitHubCopilotOAuthConfig(provider.GitHubCopilotOAuthConfig{})
	if cfg.ClientID != want {
		t.Fatalf("ClientID = %q, want %q", cfg.ClientID, want)
	}
}

func TestResolveAuth_GitHubCopilotUsesProviderAuthState(t *testing.T) {
	t.Parallel()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
		AccessToken: "gho-provider-state",
		TokenType:   "bearer",
		Scope:       "read:user",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	resolved, err := provider.ResolveAuth("github_copilot", "", raw)
	if err != nil {
		t.Fatalf("ResolveAuth error: %v", err)
	}
	if resolved.APIKey != "gho-provider-state" {
		t.Fatalf("APIKey = %q, want gho-provider-state", resolved.APIKey)
	}

	normalized, err := provider.NormalizeAuthState("github_copilot", "gho-manual", raw)
	if err != nil {
		t.Fatalf("NormalizeAuthState error: %v", err)
	}
	var state provider.GitHubCopilotAuthState
	if err := json.Unmarshal(normalized, &state); err != nil {
		t.Fatalf("unmarshal normalized state: %v", err)
	}
	if state.AccessToken != "gho-manual" {
		t.Fatalf("AccessToken = %q, want gho-manual", state.AccessToken)
	}
}

func TestGitHubCopilotProfileFiltersPickerEnabledChatModels(t *testing.T) {
	t.Parallel()

	prof, err := provider.Get("github_copilot")
	if err != nil {
		t.Fatal(err)
	}

	models, err := prof.ParseModelList(strings.NewReader(`{
		"data": [
			{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]},
			{"id":"disabled","policy":{"state":"disabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]},
			{"id":"hidden","policy":{"state":"enabled"},"model_picker_enabled":false,"supported_endpoints":["/chat/completions"]},
			{"id":"responses-only","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/responses"]},
			{"id":"","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseModelList error: %v", err)
	}

	want := []string{"gpt-4.1"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, models[i], want[i])
		}
	}
}
