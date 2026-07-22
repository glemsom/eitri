package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleProfilesBuildModelAndChatURLs(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"opencode_go", "custom_openai"} {
		prof, err := getProfile(providerID)
		if err != nil {
			t.Fatalf("getProfile(%q) error: %v", providerID, err)
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

	prof, err := getProfile("custom_openai")
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

func TestDescribe_ExposesCallerSafeMetadata(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"opencode_go":    true,
		"custom_openai":  false,
		"github_copilot": true,
	}
	for providerID, wantRequired := range cases {
		desc, err := Describe(providerID)
		if err != nil {
			t.Fatalf("Describe(%q) error: %v", providerID, err)
		}
		if desc.APIKeyRequired != wantRequired {
			t.Errorf("%s APIKeyRequired = %v, want %v", providerID, desc.APIKeyRequired, wantRequired)
		}
	}
}

func TestProfile_SupportsPromptCache(t *testing.T) {
	t.Parallel()

	expected := map[string]bool{
		"opencode_go":    true,
		"custom_openai":  true,
		"github_copilot": false,
	}

	for providerID, want := range expected {
		desc, err := Describe(providerID)
		if err != nil {
			t.Fatalf("Describe(%q) error: %v", providerID, err)
		}
		if desc.SupportsPromptCache != want {
			t.Errorf("%s SupportsPromptCache = %v, want %v", providerID, desc.SupportsPromptCache, want)
		}
	}
}

func TestGitHubCopilotProfileBuildsURLsAndHeaders(t *testing.T) {
	t.Parallel()

	prof, err := getProfile("github_copilot")
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
		"User-Agent":           "GithubCopilot/1.100.0",
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

	cfg := DefaultGitHubCopilotOAuthConfig(GitHubCopilotOAuthConfig{})
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

	cfg := DefaultGitHubCopilotOAuthConfig(GitHubCopilotOAuthConfig{})
	if cfg.ClientID != want {
		t.Fatalf("ClientID = %q, want %q", cfg.ClientID, want)
	}
}

func TestValidateCredentials_GitHubCopilotUsesProviderAuthState(t *testing.T) {
	t.Parallel()

	raw, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho-provider-state",
		TokenType:   "bearer",
		Scope:       "read:user",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	if err := ValidateCredentials("github_copilot", "", raw); err != nil {
		t.Fatalf("ValidateCredentials error: %v", err)
	}

	normalized, err := NormalizeConfigAuthState("github_copilot", "gho-manual", raw)
	if err != nil {
		t.Fatalf("NormalizeConfigAuthState error: %v", err)
	}
	var state GitHubCopilotAuthState
	if err := json.Unmarshal(normalized, &state); err != nil {
		t.Fatalf("unmarshal normalized state: %v", err)
	}
	if state.AccessToken != "gho-manual" {
		t.Fatalf("AccessToken = %q, want gho-manual", state.AccessToken)
	}
}

func TestGitHubCopilotProfileFiltersPickerEnabledChatModels(t *testing.T) {
	t.Parallel()

	prof, err := getProfile("github_copilot")
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

func TestResolveAuthForRequest_GitHubCopilotRefreshesExpiredOAuthState(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var gotGrantType string
	var gotRefreshToken string
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			http.NotFound(w, r)
			return
		}
		var reqBody map[string]string
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		gotGrantType = reqBody["grant_type"]
		gotRefreshToken = reqBody["refresh_token"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauth.Close()

	raw, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	var persistedAPIKey string
	var persistedRaw json.RawMessage
	resolved, err := resolveAuthForRequest(context.Background(), "github_copilot", "", raw, ResolveAuthOptions{
		HTTPClient: http.DefaultClient,
		GitHubCopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
		Now: now,
		Persist: func(apiKey string, raw json.RawMessage) error {
			persistedAPIKey = apiKey
			persistedRaw = append(json.RawMessage(nil), raw...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "gho-refreshed" {
		t.Fatalf("APIKey = %q, want gho-refreshed", resolved.APIKey)
	}
	if gotGrantType != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", gotGrantType)
	}
	if gotRefreshToken != "ghr-refresh" {
		t.Fatalf("refresh_token = %q, want ghr-refresh", gotRefreshToken)
	}
	if persistedAPIKey != "gho-refreshed" {
		t.Fatalf("persisted APIKey = %q, want gho-refreshed", persistedAPIKey)
	}
	var persistedState GitHubCopilotAuthState
	if err := json.Unmarshal(persistedRaw, &persistedState); err != nil {
		t.Fatalf("unmarshal persisted state: %v", err)
	}
	if persistedState.AccessToken != "gho-refreshed" {
		t.Fatalf("persisted AccessToken = %q, want gho-refreshed", persistedState.AccessToken)
	}
	if persistedState.RefreshToken != "ghr-next" {
		t.Fatalf("persisted RefreshToken = %q, want ghr-next", persistedState.RefreshToken)
	}
	if !persistedState.ExpiresAt.After(now) {
		t.Fatalf("ExpiresAt = %v, want after %v", persistedState.ExpiresAt, now)
	}
}

func TestPollGitHubCopilotDeviceFlow_ReturnsCallerSafeStatusAndAuthUpdate(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode poll request: %v", err)
		}
		if body["grant_type"] != GitHubDeviceFlowGrantType {
			t.Fatalf("grant_type = %q, want %q", body["grant_type"], GitHubDeviceFlowGrantType)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-device","token_type":"bearer","scope":"read:user"}`)
	}))
	defer oauth.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "client-id",
		AccessTokenURL: oauth.URL,
	}, "device-123", now)
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowAuthorized {
		t.Fatalf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowAuthorized)
	}
	if result.AuthUpdate == nil {
		t.Fatal("AuthUpdate = nil, want value")
	}
	if result.AuthUpdate.APIKey != "gho-device" {
		t.Fatalf("AuthUpdate.APIKey = %q, want gho-device", result.AuthUpdate.APIKey)
	}
}

func TestResolveAuthForRequest_GitHubCopilotReturnsRefreshError(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer oauth.Close()

	raw, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	_, err = resolveAuthForRequest(context.Background(), "github_copilot", "", raw, ResolveAuthOptions{
		HTTPClient: http.DefaultClient,
		GitHubCopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
		Now: now,
	})
	if err == nil {
		t.Fatal("resolveAuthForRequest error = nil, want refresh failure")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "refresh") {
		t.Fatalf("error = %q, want refresh failure", err.Error())
	}
}
