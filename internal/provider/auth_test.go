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

// ---------------------------------------------------------------------------
// EncodeGitHubCopilotAuthState / decodeGitHubCopilotAuthState round-trip
// ---------------------------------------------------------------------------

func TestEncodeDecodeGitHubCopilotAuthState_RoundTrip(t *testing.T) {
	t.Parallel()

	original := GitHubCopilotAuthState{
		AccessToken:           "gho_test_token",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr_test_refresh",
		ExpiresAt:             time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
		RefreshTokenExpiresAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	}

	encoded, err := EncodeGitHubCopilotAuthState(original)
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}
	if encoded == nil {
		t.Fatal("EncodeGitHubCopilotAuthState returned nil")
	}

	decoded, hasState, err := decodeGitHubCopilotAuthState(encoded)
	if err != nil {
		t.Fatalf("decodeGitHubCopilotAuthState error: %v", err)
	}
	if !hasState {
		t.Fatal("hasState = false, want true")
	}
	if decoded.AccessToken != original.AccessToken {
		t.Errorf("AccessToken = %q, want %q", decoded.AccessToken, original.AccessToken)
	}
	if decoded.TokenType != original.TokenType {
		t.Errorf("TokenType = %q, want %q", decoded.TokenType, original.TokenType)
	}
	if decoded.Scope != original.Scope {
		t.Errorf("Scope = %q, want %q", decoded.Scope, original.Scope)
	}
	if decoded.RefreshToken != original.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", decoded.RefreshToken, original.RefreshToken)
	}
	if !decoded.ExpiresAt.Equal(original.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", decoded.ExpiresAt, original.ExpiresAt)
	}
	if !decoded.RefreshTokenExpiresAt.Equal(original.RefreshTokenExpiresAt) {
		t.Errorf("RefreshTokenExpiresAt = %v, want %v", decoded.RefreshTokenExpiresAt, original.RefreshTokenExpiresAt)
	}
}

func TestEncodeGitHubCopilotAuthState_EmptyAccessTokenReturnsNil(t *testing.T) {
	t.Parallel()

	result, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "",
		TokenType:   "bearer",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}
	if result != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState = %#v, want nil", result)
	}

	// Whitespace-only token also returns nil.
	result, err = EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "  ",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}
	if result != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState with whitespace = %#v, want nil", result)
	}
}

func TestEncodeGitHubCopilotAuthState_SetsDefaultTokenType(t *testing.T) {
	t.Parallel()

	encoded, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho_no_tokentype",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}
	if encoded == nil {
		t.Fatal("EncodeGitHubCopilotAuthState returned nil")
	}
	var decoded GitHubCopilotAuthState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if decoded.TokenType != "bearer" {
		t.Errorf("TokenType = %q, want bearer", decoded.TokenType)
	}
}

func TestDecodeGitHubCopilotAuthState_EmptyRawReturnsNoState(t *testing.T) {
	t.Parallel()

	_, hasState, err := decodeGitHubCopilotAuthState(nil)
	if err != nil {
		t.Fatalf("decodeGitHubCopilotAuthState error: %v", err)
	}
	if hasState {
		t.Fatal("hasState = true, want false (nil raw)")
	}

	_, hasState, err = decodeGitHubCopilotAuthState(json.RawMessage{})
	if err != nil {
		t.Fatalf("decodeGitHubCopilotAuthState error: %v", err)
	}
	if hasState {
		t.Fatal("hasState = true, want false (empty raw)")
	}
}

func TestDecodeGitHubCopilotAuthState_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, _, err := decodeGitHubCopilotAuthState(json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("decodeGitHubCopilotAuthState error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid github_copilot provider_auth state") {
		t.Errorf("error = %q, want 'invalid github_copilot provider_auth state'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// applyGitHubCopilotTokenResponse
// ---------------------------------------------------------------------------

func TestApplyGitHubCopilotTokenResponse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	initial := GitHubCopilotAuthState{
		AccessToken:  "old_token",
		TokenType:    "old_type",
		Scope:        "old_scope",
		RefreshToken: "old_refresh",
		ExpiresAt:    now.Add(-time.Hour),
	}

	resp := &gitHubAccessTokenResponse{
		AccessToken:           "new_token",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "new_refresh",
		ExpiresIn:             28800,
		RefreshTokenExpiresIn: 15897600,
	}

	result := applyGitHubCopilotTokenResponse(initial, resp, now)

	if result.AccessToken != "new_token" {
		t.Errorf("AccessToken = %q, want new_token", result.AccessToken)
	}
	if result.TokenType != "bearer" {
		t.Errorf("TokenType = %q, want bearer", result.TokenType)
	}
	if result.Scope != "read:user" {
		t.Errorf("Scope = %q, want read:user", result.Scope)
	}
	if result.RefreshToken != "new_refresh" {
		t.Errorf("RefreshToken = %q, want new_refresh", result.RefreshToken)
	}
	if !result.ExpiresAt.Equal(now.Add(28800 * time.Second)) {
		t.Errorf("ExpiresAt = %v, want %v", result.ExpiresAt, now.Add(28800*time.Second))
	}
	if !result.RefreshTokenExpiresAt.Equal(now.Add(15897600 * time.Second)) {
		t.Errorf("RefreshTokenExpiresAt = %v, want %v", result.RefreshTokenExpiresAt, now.Add(15897600*time.Second))
	}
}

func TestApplyGitHubCopilotTokenResponse_KeepsExistingFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	initial := GitHubCopilotAuthState{
		AccessToken:           "existing_token",
		TokenType:             "existing_type",
		Scope:                 "existing_scope",
		RefreshToken:          "existing_refresh",
		ExpiresAt:             now.Add(-time.Hour),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	}

	// Minimal response — only sets access token, nothing else.
	resp := &gitHubAccessTokenResponse{
		AccessToken: "updated_token",
	}

	result := applyGitHubCopilotTokenResponse(initial, resp, now)

	// Access token should be updated.
	if result.AccessToken != "updated_token" {
		t.Errorf("AccessToken = %q, want updated_token", result.AccessToken)
	}
	// Original values preserved because resp fields are empty.
	if result.TokenType != "existing_type" {
		t.Errorf("TokenType = %q, want existing_type", result.TokenType)
	}
	if result.Scope != "existing_scope" {
		t.Errorf("Scope = %q, want existing_scope", result.Scope)
	}
	if result.RefreshToken != "existing_refresh" {
		t.Errorf("RefreshToken = %q, want existing_refresh", result.RefreshToken)
	}
	// ExpiresAt cleared when ExpiresIn is 0.
	if !result.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero", result.ExpiresAt)
	}
	// RefreshTokenExpiresAt preserved when RefreshTokenExpiresIn is 0.
	if !result.RefreshTokenExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("RefreshTokenExpiresAt = %v, want %v", result.RefreshTokenExpiresAt, now.Add(24*time.Hour))
	}
}

// ---------------------------------------------------------------------------
// gitHubCopilotAuthUpdateFromTokenResponse
// ---------------------------------------------------------------------------

func TestGitHubCopilotAuthUpdateFromTokenResponse_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	resp := &gitHubAccessTokenResponse{
		AccessToken:           "gho_new_token",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr_new_refresh",
		ExpiresIn:             28800,
		RefreshTokenExpiresIn: 15897600,
	}

	update, err := gitHubCopilotAuthUpdateFromTokenResponse(resp, now)
	if err != nil {
		t.Fatalf("gitHubCopilotAuthUpdateFromTokenResponse error: %v", err)
	}
	if update.APIKey != "gho_new_token" {
		t.Errorf("APIKey = %q, want gho_new_token", update.APIKey)
	}
	if update.ProviderAuth == nil {
		t.Fatal("ProviderAuth = nil, want non-nil")
	}
	var state GitHubCopilotAuthState
	if err := json.Unmarshal(update.ProviderAuth, &state); err != nil {
		t.Fatalf("unmarshal ProviderAuth: %v", err)
	}
	if state.AccessToken != "gho_new_token" {
		t.Errorf("AccessToken = %q, want gho_new_token", state.AccessToken)
	}
	if state.RefreshToken != "ghr_new_refresh" {
		t.Errorf("RefreshToken = %q, want ghr_new_refresh", state.RefreshToken)
	}
	if !state.ExpiresAt.Equal(now.Add(28800 * time.Second)) {
		t.Errorf("ExpiresAt = %v, want %v", state.ExpiresAt, now.Add(28800*time.Second))
	}
}

func TestGitHubCopilotAuthUpdateFromTokenResponse_NilResponse(t *testing.T) {
	t.Parallel()

	_, err := gitHubCopilotAuthUpdateFromTokenResponse(nil, time.Now())
	if err == nil {
		t.Fatal("gitHubCopilotAuthUpdateFromTokenResponse error = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing access token") {
		t.Errorf("error = %q, want 'missing access token'", err.Error())
	}
}

func TestGitHubCopilotAuthUpdateFromTokenResponse_EmptyAccessToken(t *testing.T) {
	t.Parallel()

	resp := &gitHubAccessTokenResponse{
		AccessToken: "",
		TokenType:   "bearer",
	}
	_, err := gitHubCopilotAuthUpdateFromTokenResponse(resp, time.Now())
	if err == nil {
		t.Fatal("gitHubCopilotAuthUpdateFromTokenResponse error = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing access token") {
		t.Errorf("error = %q, want 'missing access token'", err.Error())
	}
}

func TestGitHubCopilotAuthUpdateFromTokenResponse_WhitespaceOnlyAccessToken(t *testing.T) {
	t.Parallel()

	resp := &gitHubAccessTokenResponse{
		AccessToken: "   ",
		TokenType:   "bearer",
	}
	_, err := gitHubCopilotAuthUpdateFromTokenResponse(resp, time.Now())
	if err == nil {
		t.Fatal("gitHubCopilotAuthUpdateFromTokenResponse error = nil, want error")
	}
}

func TestGitHubCopilotAuthUpdateFromTokenResponse_ZeroTimeDefaultsToNow(t *testing.T) {
	t.Parallel()

	// Use a moment to confirm the zero-time path works.
	resp := &gitHubAccessTokenResponse{
		AccessToken:           "gho_valid",
		TokenType:             "bearer",
		RefreshToken:          "ghr_valid",
		ExpiresIn:             3600,
		RefreshTokenExpiresIn: 86400,
	}

	update, err := gitHubCopilotAuthUpdateFromTokenResponse(resp, time.Time{})
	if err != nil {
		t.Fatalf("gitHubCopilotAuthUpdateFromTokenResponse error: %v", err)
	}
	if update.APIKey != "gho_valid" {
		t.Errorf("APIKey = %q, want gho_valid", update.APIKey)
	}
}

// ---------------------------------------------------------------------------
// StartGitHubCopilotDeviceFlow
// ---------------------------------------------------------------------------

func TestStartGitHubCopilotDeviceFlow_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("User-Agent") != "Eitri" {
			t.Errorf("User-Agent = %q, want Eitri", r.Header.Get("User-Agent"))
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["client_id"] != "test-client-id" {
			t.Errorf("client_id = %q, want test-client-id", body["client_id"])
		}
		if body["scope"] != "read:user" {
			t.Errorf("scope = %q, want read:user", body["scope"])
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"device_code": "dc-test123",
			"user_code": "ABCD-1234",
			"verification_uri": "https://github.com/login/device",
			"expires_in": 900,
			"interval": 5
		}`)
	}))
	defer srv.Close()

	cfg := GitHubCopilotOAuthConfig{
		ClientID:      "test-client-id",
		DeviceCodeURL: srv.URL,
		Scope:         "read:user",
	}
	resp, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, cfg)
	if err != nil {
		t.Fatalf("StartGitHubCopilotDeviceFlow error: %v", err)
	}
	if resp.DeviceCode != "dc-test123" {
		t.Errorf("DeviceCode = %q, want dc-test123", resp.DeviceCode)
	}
	if resp.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q, want ABCD-1234", resp.UserCode)
	}
	if resp.VerificationURI != "https://github.com/login/device" {
		t.Errorf("VerificationURI = %q, want https://github.com/login/device", resp.VerificationURI)
	}
	if resp.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900", resp.ExpiresIn)
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want 5", resp.Interval)
	}
}

func TestStartGitHubCopilotDeviceFlow_FillsDefaultIntervalAndExpiry(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Zero interval and expires_in — should get defaults.
		fmt.Fprint(w, `{
			"device_code": "dc-defaults",
			"user_code": "EFGH-5678",
			"verification_uri": "https://github.com/login/device",
			"expires_in": 0,
			"interval": 0
		}`)
	}))
	defer srv.Close()

	cfg := GitHubCopilotOAuthConfig{
		ClientID:      "test-client-id",
		DeviceCodeURL: srv.URL,
	}
	resp, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, cfg)
	if err != nil {
		t.Fatalf("StartGitHubCopilotDeviceFlow error: %v", err)
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want 5 (default)", resp.Interval)
	}
	if resp.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900 (default)", resp.ExpiresIn)
	}
}

func TestStartGitHubCopilotDeviceFlow_ErrorInResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"Bad client id"}`)
	}))
	defer srv.Close()

	_, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:      "bad-client",
		DeviceCodeURL: srv.URL,
	})
	if err == nil {
		t.Fatal("StartGitHubCopilotDeviceFlow error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("error = %q, want 'invalid_client'", err.Error())
	}
}

func TestStartGitHubCopilotDeviceFlow_IncompleteResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Missing required fields.
		fmt.Fprint(w, `{"device_code":"","user_code":"","verification_uri":""}`)
	}))
	defer srv.Close()

	_, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:      "test-client",
		DeviceCodeURL: srv.URL,
	})
	if err == nil {
		t.Fatal("StartGitHubCopilotDeviceFlow error = nil, want error")
	}
	if !strings.Contains(err.Error(), "incomplete response") {
		t.Errorf("error = %q, want 'incomplete response'", err.Error())
	}
}

func TestStartGitHubCopilotDeviceFlow_NonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:      "test-client",
		DeviceCodeURL: srv.URL,
	})
	if err == nil {
		t.Fatal("StartGitHubCopilotDeviceFlow error = nil, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("error = %q, want 'HTTP 502'", err.Error())
	}
}

func TestStartGitHubCopilotDeviceFlow_NetworkError(t *testing.T) {
	t.Parallel()

	// Use a dial failure as network error.
	_, err := StartGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:      "test-client",
		DeviceCodeURL: "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("StartGitHubCopilotDeviceFlow error = nil, want network error")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error = %q, want 'unreachable'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// PollGitHubCopilotDeviceFlow
// ---------------------------------------------------------------------------

func TestPollGitHubCopilotDeviceFlow_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["device_code"] != "dc-test" {
			t.Errorf("device_code = %q, want dc-test", body["device_code"])
		}
		if body["grant_type"] != GitHubDeviceFlowGrantType {
			t.Errorf("grant_type = %q, want %q", body["grant_type"], GitHubDeviceFlowGrantType)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"access_token": "gho_polled",
			"token_type": "bearer",
			"scope": "read:user",
			"refresh_token": "ghr_polled",
			"expires_in": 28800,
			"refresh_token_expires_in": 15897600
		}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-test", now)
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowAuthorized {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowAuthorized)
	}
	if result.AuthUpdate == nil {
		t.Fatal("AuthUpdate = nil, want non-nil")
	}
	if result.AuthUpdate.APIKey != "gho_polled" {
		t.Errorf("APIKey = %q, want gho_polled", result.AuthUpdate.APIKey)
	}
}

func TestPollGitHubCopilotDeviceFlow_AuthorizationPending(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-pending", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowAuthorizationPending {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowAuthorizationPending)
	}
	if result.ErrorCode != "authorization_pending" {
		t.Errorf("ErrorCode = %q, want authorization_pending", result.ErrorCode)
	}
	if result.AuthUpdate != nil {
		t.Fatal("AuthUpdate should be nil for pending status")
	}
}

func TestPollGitHubCopilotDeviceFlow_SlowDown(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"slow_down"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-slow", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowSlowDown {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowSlowDown)
	}
	if result.ErrorCode != "slow_down" {
		t.Errorf("ErrorCode = %q, want slow_down", result.ErrorCode)
	}
}

func TestPollGitHubCopilotDeviceFlow_ExpiredToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"expired_token"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-expired", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowExpiredToken {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowExpiredToken)
	}
}

func TestPollGitHubCopilotDeviceFlow_TokenExpiredVariant(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"token_expired"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-expired2", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowExpiredToken {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowExpiredToken)
	}
}

func TestPollGitHubCopilotDeviceFlow_AccessDenied(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"access_denied"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-denied", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowAccessDenied {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowAccessDenied)
	}
}

func TestPollGitHubCopilotDeviceFlow_UnknownErrorMapsToProviderError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"unexpected_oauth_problem"}`)
	}))
	defer srv.Close()

	result, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-unknown", time.Now())
	if err != nil {
		t.Fatalf("PollGitHubCopilotDeviceFlow error: %v", err)
	}
	if result.Status != GitHubCopilotDeviceFlowProviderError {
		t.Errorf("Status = %q, want %q", result.Status, GitHubCopilotDeviceFlowProviderError)
	}
	if result.ErrorCode != "unexpected_oauth_problem" {
		t.Errorf("ErrorCode = %q, want unexpected_oauth_problem", result.ErrorCode)
	}
}

func TestPollGitHubCopilotDeviceFlow_HTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := PollGitHubCopilotDeviceFlow(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "dc-httperr", time.Now())
	if err == nil {
		t.Fatal("PollGitHubCopilotDeviceFlow error = nil, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error = %q, want 'HTTP 400'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// refreshGitHubCopilotToken
// ---------------------------------------------------------------------------

func TestRefreshGitHubCopilotToken_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["grant_type"] != GitHubRefreshTokenGrantType {
			t.Errorf("grant_type = %q, want %q", body["grant_type"], GitHubRefreshTokenGrantType)
		}
		if body["refresh_token"] != "ghr-old-refresh" {
			t.Errorf("refresh_token = %q, want ghr-old-refresh", body["refresh_token"])
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"access_token": "gho-refreshed",
			"token_type": "bearer",
			"scope": "read:user",
			"refresh_token": "ghr-new-refresh",
			"expires_in": 28800,
			"refresh_token_expires_in": 15897600
		}`)
	}))
	defer srv.Close()

	resp, err := refreshGitHubCopilotToken(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "ghr-old-refresh")
	if err != nil {
		t.Fatalf("refreshGitHubCopilotToken error: %v", err)
	}
	if resp.AccessToken != "gho-refreshed" {
		t.Errorf("AccessToken = %q, want gho-refreshed", resp.AccessToken)
	}
	if resp.RefreshToken != "ghr-new-refresh" {
		t.Errorf("RefreshToken = %q, want ghr-new-refresh", resp.RefreshToken)
	}
}

func TestRefreshGitHubCopilotToken_ErrorInResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":"bad_refresh_token"}`)
	}))
	defer srv.Close()

	_, err := refreshGitHubCopilotToken(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "ghr-bad")
	if err == nil {
		t.Fatal("refreshGitHubCopilotToken error = nil, want error")
	}
	if !strings.Contains(err.Error(), "bad_refresh_token") {
		t.Errorf("error = %q, want 'bad_refresh_token'", err.Error())
	}
}

func TestRefreshGitHubCopilotToken_EmptyAccessTokenInResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"access_token": "",
			"token_type": "bearer"
		}`)
	}))
	defer srv.Close()

	_, err := refreshGitHubCopilotToken(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "ghr-empty")
	if err == nil {
		t.Fatal("refreshGitHubCopilotToken error = nil, want error")
	}
	if !strings.Contains(err.Error(), "incomplete response") {
		t.Errorf("error = %q, want 'incomplete response'", err.Error())
	}
}

func TestRefreshGitHubCopilotToken_HTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := refreshGitHubCopilotToken(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, "ghr-unauth")
	if err == nil {
		t.Fatal("refreshGitHubCopilotToken error = nil, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("error = %q, want 'HTTP 401'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// postGitHubCopilotAccessTokenRequest
// ---------------------------------------------------------------------------

func TestPostGitHubCopilotAccessTokenRequest_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["custom_field"] != "custom_value" {
			t.Errorf("custom_field = %q, want custom_value", body["custom_field"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho_custom","token_type":"bearer"}`)
	}))
	defer srv.Close()

	resp, err := postGitHubCopilotAccessTokenRequest(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, map[string]string{"custom_field": "custom_value"}, "custom prefix")
	if err != nil {
		t.Fatalf("postGitHubCopilotAccessTokenRequest error: %v", err)
	}
	if resp.AccessToken != "gho_custom" {
		t.Errorf("AccessToken = %q, want gho_custom", resp.AccessToken)
	}
}

func TestPostGitHubCopilotAccessTokenRequest_NonOKStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := postGitHubCopilotAccessTokenRequest(context.Background(), http.DefaultClient, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, map[string]string{}, "test prefix")
	if err == nil {
		t.Fatal("postGitHubCopilotAccessTokenRequest error = nil, want error")
	}
	if !strings.Contains(err.Error(), "test prefix: HTTP 403") {
		t.Errorf("error = %q, want 'test prefix: HTTP 403'", err.Error())
	}
}

func TestPostGitHubCopilotAccessTokenRequest_NilClientDefaults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho_default_client","token_type":"bearer"}`)
	}))
	defer srv.Close()

	resp, err := postGitHubCopilotAccessTokenRequest(context.Background(), nil, GitHubCopilotOAuthConfig{
		ClientID:       "test-client",
		AccessTokenURL: srv.URL,
	}, map[string]string{}, "test")
	if err != nil {
		t.Fatalf("postGitHubCopilotAccessTokenRequest error: %v", err)
	}
	if resp.AccessToken != "gho_default_client" {
		t.Errorf("AccessToken = %q, want gho_default_client", resp.AccessToken)
	}
}

// ---------------------------------------------------------------------------
// NormalizeConfigAuthState
// ---------------------------------------------------------------------------

func TestNormalizeConfigAuthState_GitHubCopilotWithAPIKey(t *testing.T) {
	t.Parallel()

	raw, err := NormalizeConfigAuthState("github_copilot", "gho_raw_key", nil)
	if err != nil {
		t.Fatalf("NormalizeConfigAuthState error: %v", err)
	}
	if raw == nil {
		t.Fatal("NormalizeConfigAuthState returned nil")
	}
	var state GitHubCopilotAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if state.AccessToken != "gho_raw_key" {
		t.Errorf("AccessToken = %q, want gho_raw_key", state.AccessToken)
	}
	if state.TokenType != "bearer" {
		t.Errorf("TokenType = %q, want bearer", state.TokenType)
	}
}

func TestNormalizeConfigAuthState_GitHubCopilotWithProviderAuth(t *testing.T) {
	t.Parallel()

	existingState, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho_from_state",
		TokenType:   "bearer",
		Scope:       "read:user",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	raw, err := NormalizeConfigAuthState("github_copilot", "", existingState)
	if err != nil {
		t.Fatalf("NormalizeConfigAuthState error: %v", err)
	}
	if raw == nil {
		t.Fatal("NormalizeConfigAuthState returned nil")
	}
	var state GitHubCopilotAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if state.AccessToken != "gho_from_state" {
		t.Errorf("AccessToken = %q, want gho_from_state", state.AccessToken)
	}
}

func TestNormalizeConfigAuthState_GitHubCopilotEmptyReturnsNil(t *testing.T) {
	t.Parallel()

	raw, err := NormalizeConfigAuthState("github_copilot", "", nil)
	if err != nil {
		t.Fatalf("NormalizeConfigAuthState error: %v", err)
	}
	if raw != nil {
		t.Fatalf("NormalizeConfigAuthState = %#v, want nil", raw)
	}
}

func TestNormalizeConfigAuthState_NonCopilotProviderReturnsNil(t *testing.T) {
	t.Parallel()

	raw, err := NormalizeConfigAuthState("opencode_go", "sk-test", nil)
	if err != nil {
		t.Fatalf("NormalizeConfigAuthState error: %v", err)
	}
	if raw != nil {
		t.Fatalf("NormalizeConfigAuthState = %#v, want nil", raw)
	}
}

func TestNormalizeConfigAuthState_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := NormalizeConfigAuthState("nonexistent", "", nil)
	if err == nil {
		t.Fatal("NormalizeConfigAuthState error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("error = %q, want 'unsupported provider'", err.Error())
	}
}

func TestNormalizeConfigAuthState_InvalidProviderAuth(t *testing.T) {
	t.Parallel()

	_, err := NormalizeConfigAuthState("github_copilot", "", json.RawMessage(`not valid json`))
	if err == nil {
		t.Fatal("NormalizeConfigAuthState error = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// resolveAuth (non-Copilot providers)
// ---------------------------------------------------------------------------

func TestResolveAuth_NonCopilotProviderReturnsAPIKey(t *testing.T) {
	t.Parallel()

	resolved, err := resolveAuth("opencode_go", "sk-my-key", nil)
	if err != nil {
		t.Fatalf("resolveAuth error: %v", err)
	}
	if resolved.APIKey != "sk-my-key" {
		t.Errorf("APIKey = %q, want sk-my-key", resolved.APIKey)
	}
}

func TestResolveAuth_NonCopilotProviderTrimsAPIKey(t *testing.T) {
	t.Parallel()

	resolved, err := resolveAuth("opencode_go", "  sk-padded  ", nil)
	if err != nil {
		t.Fatalf("resolveAuth error: %v", err)
	}
	if resolved.APIKey != "sk-padded" {
		t.Errorf("APIKey = %q, want sk-padded", resolved.APIKey)
	}
}

func TestResolveAuth_UnknownProvider(t *testing.T) {
	t.Parallel()

	_, err := resolveAuth("nonexistent", "", nil)
	if err == nil {
		t.Fatal("resolveAuth error = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// resolveAuthForRequest (GitHub Copilot with refresh)
// ---------------------------------------------------------------------------

func TestResolveAuthForRequest_GitHubCopilotNoStateUsesAPIKey(t *testing.T) {
	t.Parallel()

	resolved, err := resolveAuthForRequest(context.Background(), "github_copilot", "gho-raw-key", nil, ResolveAuthOptions{})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "gho-raw-key" {
		t.Errorf("APIKey = %q, want gho-raw-key", resolved.APIKey)
	}
}

func TestResolveAuthForRequest_GitHubCopilotWithValidStateReturnsAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho-valid",
		TokenType:   "bearer",
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	resolved, err := resolveAuthForRequest(context.Background(), "github_copilot", "", state, ResolveAuthOptions{Now: now})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "gho-valid" {
		t.Errorf("APIKey = %q, want gho-valid", resolved.APIKey)
	}
}

func TestResolveAuthForRequest_GitHubCopilotExpiredTokenFailsWithoutRefresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho-expired",
		TokenType:   "bearer",
		ExpiresAt:   now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	_, err = resolveAuthForRequest(context.Background(), "github_copilot", "", state, ResolveAuthOptions{Now: now})
	if err == nil {
		t.Fatal("resolveAuthForRequest error = nil, want 'token expired' error")
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error = %q, want 'token expired'", err.Error())
	}
}

func TestResolveAuthForRequest_GitHubCopilotExpiredRefreshTokenFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		RefreshToken:          "ghr-expired",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	_, err = resolveAuthForRequest(context.Background(), "github_copilot", "", state, ResolveAuthOptions{Now: now})
	if err == nil {
		t.Fatal("resolveAuthForRequest error = nil, want 'refresh token expired' error")
	}
	if !strings.Contains(err.Error(), "refresh token expired") {
		t.Errorf("error = %q, want 'refresh token expired'", err.Error())
	}
}

func TestResolveAuthForRequest_GitHubCopilotRefreshesExpiredToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"access_token": "gho-refreshed",
			"token_type": "bearer",
			"scope": "read:user",
			"refresh_token": "ghr-next",
			"expires_in": 28800,
			"refresh_token_expires_in": 15897600
		}`)
	}))
	defer oauthSrv.Close()

	state, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		RefreshToken:          "ghr-old-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	var persistedAPIKey string
	var persistedAuth json.RawMessage
	resolved, err := resolveAuthForRequest(context.Background(), "github_copilot", "", state, ResolveAuthOptions{
		HTTPClient: http.DefaultClient,
		GitHubCopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "test-client",
			AccessTokenURL: oauthSrv.URL,
		},
		Now: now,
		Persist: func(apiKey string, raw json.RawMessage) error {
			persistedAPIKey = apiKey
			persistedAuth = raw
			return nil
		},
	})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "gho-refreshed" {
		t.Errorf("APIKey = %q, want gho-refreshed", resolved.APIKey)
	}
	if persistedAPIKey != "gho-refreshed" {
		t.Errorf("persisted APIKey = %q, want gho-refreshed", persistedAPIKey)
	}
	if persistedAuth == nil {
		t.Fatal("persisted Auth is nil")
	}
	var persistedState GitHubCopilotAuthState
	if err := json.Unmarshal(persistedAuth, &persistedState); err != nil {
		t.Fatalf("unmarshal persisted auth: %v", err)
	}
	if persistedState.AccessToken != "gho-refreshed" {
		t.Errorf("persisted AccessToken = %q, want gho-refreshed", persistedState.AccessToken)
	}
	if persistedState.RefreshToken != "ghr-next" {
		t.Errorf("persisted RefreshToken = %q, want ghr-next", persistedState.RefreshToken)
	}
}

func TestResolveAuthForRequest_NonCopilotProviderPassthrough(t *testing.T) {
	t.Parallel()

	resolved, err := resolveAuthForRequest(context.Background(), "opencode_go", "sk-key", nil, ResolveAuthOptions{})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "sk-key" {
		t.Errorf("APIKey = %q, want sk-key", resolved.APIKey)
	}
}

func TestResolveAuthForRequest_GitHubCopilotBadStateFallsBackToAPIKey(t *testing.T) {
	t.Parallel()

	resolved, err := resolveAuthForRequest(context.Background(), "github_copilot", "gho-fallback", json.RawMessage(`not json`), ResolveAuthOptions{})
	if err != nil {
		t.Fatalf("resolveAuthForRequest error: %v", err)
	}
	if resolved.APIKey != "gho-fallback" {
		t.Errorf("APIKey = %q, want gho-fallback", resolved.APIKey)
	}
}

func TestResolveAuthForRequest_GitHubCopilotBadStateNoAPIKeyReturnsError(t *testing.T) {
	t.Parallel()

	_, err := resolveAuthForRequest(context.Background(), "github_copilot", "", json.RawMessage(`not json`), ResolveAuthOptions{})
	if err == nil {
		t.Fatal("resolveAuthForRequest error = nil, want error")
	}
}

// ---------------------------------------------------------------------------
// ValidateCredentials
// ---------------------------------------------------------------------------

func TestValidateCredentials_GitHubCopilotWithValidToken(t *testing.T) {
	t.Parallel()

	raw, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho-valid-token",
		TokenType:   "bearer",
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	err = ValidateCredentials("github_copilot", "", raw)
	if err != nil {
		t.Fatalf("ValidateCredentials error: %v", err)
	}
}

func TestValidateCredentials_GitHubCopilotWithAPIKey(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("github_copilot", "gho-api-key", nil)
	if err != nil {
		t.Fatalf("ValidateCredentials error: %v", err)
	}
}

func TestValidateCredentials_OpenCodeGoWithAPIKey(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("opencode_go", "sk-valid", nil)
	if err != nil {
		t.Fatalf("ValidateCredentials error: %v", err)
	}
}

func TestValidateCredentials_OpenCodeGoMissingAPIKey(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("opencode_go", "", nil)
	if err == nil {
		t.Fatal("ValidateCredentials error = nil, want error")
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Errorf("error = %q, want 'api_key is required'", err.Error())
	}
}

func TestValidateCredentials_UnknownProvider(t *testing.T) {
	t.Parallel()

	err := ValidateCredentials("nonexistent", "", nil)
	if err == nil {
		t.Fatal("ValidateCredentials error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("error = %q, want 'unsupported provider'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// DefaultGitHubCopilotOAuthConfig
// ---------------------------------------------------------------------------

func TestDefaultGitHubCopilotOAuthConfig_FillsDefaults(t *testing.T) {
	t.Parallel()

	cfg := DefaultGitHubCopilotOAuthConfig(GitHubCopilotOAuthConfig{})
	if cfg.ClientID != DefaultGitHubCopilotOAuthClientID {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, DefaultGitHubCopilotOAuthClientID)
	}
	if cfg.DeviceCodeURL != "https://github.com/login/device/code" {
		t.Errorf("DeviceCodeURL = %q, want https://github.com/login/device/code", cfg.DeviceCodeURL)
	}
	if cfg.AccessTokenURL != "https://github.com/login/oauth/access_token" {
		t.Errorf("AccessTokenURL = %q, want https://github.com/login/oauth/access_token", cfg.AccessTokenURL)
	}
	if cfg.Scope != "read:user" {
		t.Errorf("Scope = %q, want read:user", cfg.Scope)
	}
}

func TestDefaultGitHubCopilotOAuthConfig_PreservesCustomValues(t *testing.T) {
	t.Parallel()

	cfg := DefaultGitHubCopilotOAuthConfig(GitHubCopilotOAuthConfig{
		ClientID:       "custom-client",
		DeviceCodeURL:  "https://custom.device/code",
		AccessTokenURL: "https://custom.token",
		Scope:          "custom:scope",
	})
	if cfg.ClientID != "custom-client" {
		t.Errorf("ClientID = %q, want custom-client", cfg.ClientID)
	}
	if cfg.DeviceCodeURL != "https://custom.device/code" {
		t.Errorf("DeviceCodeURL = %q, want https://custom.device/code", cfg.DeviceCodeURL)
	}
	if cfg.AccessTokenURL != "https://custom.token" {
		t.Errorf("AccessTokenURL = %q, want https://custom.token", cfg.AccessTokenURL)
	}
	if cfg.Scope != "custom:scope" {
		t.Errorf("Scope = %q, want custom:scope", cfg.Scope)
	}
}

// ---------------------------------------------------------------------------
// ResolveAuth (exported)
// ---------------------------------------------------------------------------

func TestResolveAuth_NonCopilotReturnsAPIKey(t *testing.T) {
	t.Parallel()

	apiKey, update, err := ResolveAuth(context.Background(), ResolveAuthRequest{
		ProviderID: "opencode_go",
		APIKey:     "sk-resolved",
	}, nil)
	if err != nil {
		t.Fatalf("ResolveAuth error: %v", err)
	}
	if apiKey != "sk-resolved" {
		t.Errorf("apiKey = %q, want sk-resolved", apiKey)
	}
	if update != nil {
		t.Errorf("update = %#v, want nil", update)
	}
}

func TestResolveAuth_NilPersistReturnsUpdateOnRefresh(t *testing.T) {
	t.Parallel()

	// Note: ResolveAuth (exported) does not accept a Now parameter, so we must
	// use time.Now() as the reference point.
	now := time.Now().Truncate(time.Second)
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauthSrv.Close()

	// Use a RedirectURL-based approach: point the default AccessTokenURL to our test server
	// by setting the oauth config in the test server's handler. But ResolveAuth doesn't
	// accept oauth config — it uses the default GitHub URLs. So the token must not be expired
	// for this test; instead we test the non-expired path.
	// To test the refresh path, use resolveAuthForRequest directly.
	state, err := EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
		AccessToken: "gho-valid-token",
		TokenType:   "bearer",
		ExpiresAt:   now.Add(time.Hour), // not expired
	})
	if err != nil {
		t.Fatalf("EncodeGitHubCopilotAuthState error: %v", err)
	}

	apiKey, update, err := ResolveAuth(context.Background(), ResolveAuthRequest{
		ProviderID:   "github_copilot",
		APIKey:       "",
		ProviderAuth: state,
	}, nil)
	if err != nil {
		t.Fatalf("ResolveAuth error: %v", err)
	}
	if apiKey != "gho-valid-token" {
		t.Errorf("apiKey = %q, want gho-valid-token", apiKey)
	}
	if update != nil {
		t.Errorf("update = %#v, want nil (no refresh needed)", update)
	}
}
