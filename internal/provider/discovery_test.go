package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

func TestDiscoverModels_OpenAICompatibleReturnsSelectableModels(t *testing.T) {
	t.Parallel()

	var gotAuth string
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-4.1"},{"id":""},{"id":"gpt-4o-mini"}]}`)
	}))
	defer modelsSrv.Close()

	result, err := provider.DiscoverModels(context.Background(), provider.DiscoveryRequest{
		ProviderID: "opencode_go",
		BaseURL:    modelsSrv.URL + "/v1",
		APIKey:     "sk-test",
	}, provider.DiscoveryOptions{HTTPClient: http.DefaultClient})
	if err != nil {
		t.Fatalf("DiscoverModels error: %v", err)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	want := []string{"gpt-4.1", "gpt-4o-mini"}
	if len(result.Models) != len(want) {
		t.Fatalf("Models = %#v, want %#v", result.Models, want)
	}
	for i := range want {
		if result.Models[i] != want[i] {
			t.Errorf("Models[%d] = %q, want %q", i, result.Models[i], want[i])
		}
	}
	if result.AuthUpdate != nil {
		t.Fatalf("AuthUpdate = %#v, want nil", result.AuthUpdate)
	}
}

func TestDiscoverModels_GitHubCopilotRefreshesExpiredAuthAndReturnsUpdate(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var gotGrantType string
	var gotRefreshToken string
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		gotGrantType = body["grant_type"]
		gotRefreshToken = body["refresh_token"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauthSrv.Close()

	var gotModelsAuth string
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotModelsAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]},{"id":"hidden","policy":{"state":"enabled"},"model_picker_enabled":false,"supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer modelsSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
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

	result, err := provider.DiscoverModels(context.Background(), provider.DiscoveryRequest{
		ProviderID:   "github_copilot",
		BaseURL:      modelsSrv.URL,
		ProviderAuth: raw,
	}, provider.DiscoveryOptions{
		HTTPClient: http.DefaultClient,
		GitHubCopilotOAuth: provider.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauthSrv.URL,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("DiscoverModels error: %v", err)
	}
	if gotGrantType != provider.GitHubRefreshTokenGrantType {
		t.Fatalf("grant_type = %q, want %q", gotGrantType, provider.GitHubRefreshTokenGrantType)
	}
	if gotRefreshToken != "ghr-refresh" {
		t.Fatalf("refresh_token = %q, want ghr-refresh", gotRefreshToken)
	}
	if gotModelsAuth != "Bearer gho-refreshed" {
		t.Fatalf("models Authorization = %q, want Bearer gho-refreshed", gotModelsAuth)
	}
	want := []string{"gpt-4.1"}
	if len(result.Models) != len(want) {
		t.Fatalf("Models = %#v, want %#v", result.Models, want)
	}
	for i := range want {
		if result.Models[i] != want[i] {
			t.Errorf("Models[%d] = %q, want %q", i, result.Models[i], want[i])
		}
	}
	if result.AuthUpdate == nil {
		t.Fatal("AuthUpdate = nil, want refreshed auth state")
	}
	if result.AuthUpdate.APIKey != "gho-refreshed" {
		t.Fatalf("AuthUpdate.APIKey = %q, want gho-refreshed", result.AuthUpdate.APIKey)
	}
	var refreshed provider.GitHubCopilotAuthState
	if err := json.Unmarshal(result.AuthUpdate.ProviderAuth, &refreshed); err != nil {
		t.Fatalf("unmarshal AuthUpdate.ProviderAuth: %v", err)
	}
	if refreshed.AccessToken != "gho-refreshed" {
		t.Fatalf("refreshed AccessToken = %q, want gho-refreshed", refreshed.AccessToken)
	}
	if refreshed.RefreshToken != "ghr-next" {
		t.Fatalf("refreshed RefreshToken = %q, want ghr-next", refreshed.RefreshToken)
	}
}

func TestDiscoverModels_MapsProviderAuthFailureToFriendlyError(t *testing.T) {
	t.Parallel()

	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer modelsSrv.Close()

	_, err := provider.DiscoverModels(context.Background(), provider.DiscoveryRequest{
		ProviderID: "custom_openai",
		BaseURL:    modelsSrv.URL,
		APIKey:     "sk-bad",
	}, provider.DiscoveryOptions{HTTPClient: http.DefaultClient})
	if err == nil {
		t.Fatal("DiscoverModels error = nil, want auth failure")
	}
	if !strings.Contains(err.Error(), "Provider authentication failed") {
		t.Fatalf("error = %q, want friendly auth failure", err.Error())
	}
}
