package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func collectChatResponses(seq func(func(*model.LLMResponse, error) bool)) (responses []*model.LLMResponse, lastErr error) {
	for resp, err := range seq {
		if err != nil {
			lastErr = err
			continue
		}
		if resp != nil {
			responses = append(responses, resp)
		}
	}
	return
}

func TestNewChatModel_OpenAICompatibleReturnsReadyToUseModel(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuth string
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"ready"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	result, err := provider.NewChatModel(context.Background(), provider.ChatRequest{
		ProviderID: "opencode_go",
		BaseURL:    chatSrv.URL + "/v1",
		APIKey:     "sk-test",
		Model:      "gpt-4.1",
	}, provider.ChatOptions{})
	if err != nil {
		t.Fatalf("NewChatModel error: %v", err)
	}
	if result.AuthUpdate != nil {
		t.Fatalf("AuthUpdate = %#v, want nil", result.AuthUpdate)
	}

	responses, err := collectChatResponses(result.Model.GenerateContent(context.Background(), &model.LLMRequest{
		Model:    "gpt-4.1",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}},
	}, true))
	if err != nil {
		t.Fatalf("GenerateContent error: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	if len(responses) == 0 || responses[0].Content == nil || len(responses[0].Content.Parts) == 0 || responses[0].Content.Parts[0].Text != "ready" {
		t.Fatalf("responses = %+v, want first text chunk ready", responses)
	}
}

func TestNewChatModel_PersistAuthCallbackInvokedOnGitHubCopilotRefresh(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-persisted","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauthSrv.Close()

	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"ok"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	var persistCalled int
	var persistAPIKey string
	var persistProviderAuth json.RawMessage
	result, err := provider.NewChatModel(context.Background(), provider.ChatRequest{
		ProviderID:   "github_copilot",
		BaseURL:      chatSrv.URL,
		ProviderAuth: raw,
		Model:        "gpt-4.1",
	}, provider.ChatOptions{
		GitHubCopilotOAuth: provider.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauthSrv.URL,
		},
		Now: now,
		PersistAuth: func(apiKey string, providerAuth json.RawMessage) error {
			persistCalled++
			persistAPIKey = apiKey
			persistProviderAuth = providerAuth
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewChatModel error: %v", err)
	}
	if result.AuthUpdate != nil {
		t.Fatalf("AuthUpdate = %#v, want nil when PersistAuth is set", result.AuthUpdate)
	}
	if persistCalled != 1 {
		t.Fatalf("PersistAuth called %d times, want 1", persistCalled)
	}
	if persistAPIKey != "gho-persisted" {
		t.Fatalf("PersistAuth APIKey = %q, want gho-persisted", persistAPIKey)
	}
	var state provider.GitHubCopilotAuthState
	if err := json.Unmarshal(persistProviderAuth, &state); err != nil {
		t.Fatalf("unmarshal ProviderAuth: %v", err)
	}
	if state.AccessToken != "gho-persisted" {
		t.Fatalf("AccessToken = %q, want gho-persisted", state.AccessToken)
	}
}

func TestNewChatModel_NilPersistAuthDoesNotCrashAndReturnsAuthUpdate(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauthSrv.Close()

	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"ok"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	// PersistAuth is nil (zero value)
	result, err := provider.NewChatModel(context.Background(), provider.ChatRequest{
		ProviderID:   "github_copilot",
		BaseURL:      chatSrv.URL,
		ProviderAuth: raw,
		Model:        "gpt-4.1",
	}, provider.ChatOptions{
		GitHubCopilotOAuth: provider.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauthSrv.URL,
		},
		Now: now,
		// PersistAuth not set — nil
	})
	if err != nil {
		t.Fatalf("NewChatModel error: %v", err)
	}
	if result.AuthUpdate == nil {
		t.Fatal("AuthUpdate = nil, want refreshed auth state when PersistAuth is nil")
	}
	if result.AuthUpdate.APIKey != "gho-refreshed" {
		t.Fatalf("AuthUpdate.APIKey = %q, want gho-refreshed", result.AuthUpdate.APIKey)
	}
}

func TestNewChatModel_GitHubCopilotRefreshesExpiredAuthAndReturnsUpdate(t *testing.T) {
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

	var gotPath string
	var gotAuth string
	var gotIntent string
	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotIntent = r.Header.Get("Openai-Intent")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"copilot"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chatSrv.Close()

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

	result, err := provider.NewChatModel(context.Background(), provider.ChatRequest{
		ProviderID:   "github_copilot",
		BaseURL:      chatSrv.URL,
		ProviderAuth: raw,
		Model:        "gpt-4.1",
	}, provider.ChatOptions{
		GitHubCopilotOAuth: provider.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauthSrv.URL,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("NewChatModel error: %v", err)
	}
	if result.AuthUpdate == nil {
		t.Fatal("AuthUpdate = nil, want refreshed auth state")
	}
	if result.AuthUpdate.APIKey != "gho-refreshed" {
		t.Fatalf("AuthUpdate.APIKey = %q, want gho-refreshed", result.AuthUpdate.APIKey)
	}

	responses, err := collectChatResponses(result.Model.GenerateContent(context.Background(), &model.LLMRequest{
		Model:    "gpt-4.1",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}},
	}, true))
	if err != nil {
		t.Fatalf("GenerateContent error: %v", err)
	}
	if gotGrantType != provider.GitHubRefreshTokenGrantType {
		t.Fatalf("grant_type = %q, want %q", gotGrantType, provider.GitHubRefreshTokenGrantType)
	}
	if gotRefreshToken != "ghr-refresh" {
		t.Fatalf("refresh_token = %q, want ghr-refresh", gotRefreshToken)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer gho-refreshed" {
		t.Fatalf("Authorization = %q, want Bearer gho-refreshed", gotAuth)
	}
	if gotIntent != "conversation-panel" {
		t.Fatalf("Openai-Intent = %q, want conversation-panel", gotIntent)
	}
	if len(responses) == 0 || responses[0].Content == nil || len(responses[0].Content.Parts) == 0 || responses[0].Content.Parts[0].Text != "copilot" {
		t.Fatalf("responses = %+v, want first text chunk copilot", responses)
	}
}
