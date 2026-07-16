package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const GitHubDeviceFlowGrantType = "urn:ietf:params:oauth:grant-type:device_code"
const GitHubRefreshTokenGrantType = "refresh_token"
const DefaultGitHubCopilotOAuthClientID = "Iv1.b507a08c87ecfe98"

// PersistAuthFunc persists refreshed provider auth state (api key + provider_auth JSON).
// Called during auth refresh before the provider operation returns.
type PersistAuthFunc func(apiKey string, providerAuth json.RawMessage) error

// ResolvedAuth holds provider-specific auth material normalized for requests.
type ResolvedAuth struct {
	APIKey string
}

type authHandler interface {
	Normalize(apiKey string, raw json.RawMessage) (json.RawMessage, error)
	Resolve(apiKey string, raw json.RawMessage) (ResolvedAuth, error)
}

// GitHubCopilotAuthState stores provider-owned Copilot credential state.
type GitHubCopilotAuthState struct {
	AccessToken           string    `json:"access_token"`
	TokenType             string    `json:"token_type,omitempty"`
	Scope                 string    `json:"scope,omitempty"`
	RefreshToken          string    `json:"refresh_token,omitempty"`
	ExpiresAt             time.Time `json:"expires_at,omitempty"`
	RefreshTokenExpiresAt time.Time `json:"refresh_token_expires_at,omitempty"`
}

// GitHubCopilotOAuthConfig configures GitHub OAuth device-flow endpoints.
type GitHubCopilotOAuthConfig struct {
	ClientID       string
	DeviceCodeURL  string
	AccessTokenURL string
	Scope          string
}

// GitHubDeviceCodeResponse is GitHub device-flow start response.
type GitHubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
}

type gitHubAccessTokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	Scope                 string `json:"scope"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	ExpiresIn             int    `json:"expires_in,omitempty"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in,omitempty"`
	Error                 string `json:"error"`
}

// DefaultGitHubCopilotOAuthConfig fills default GitHub OAuth endpoints and scope.
func DefaultGitHubCopilotOAuthConfig(cfg GitHubCopilotOAuthConfig) GitHubCopilotOAuthConfig {
	if cfg.ClientID == "" {
		cfg.ClientID = strings.TrimSpace(os.Getenv("EITRI_GITHUB_CLIENT_ID"))
	}
	if cfg.ClientID == "" {
		cfg.ClientID = DefaultGitHubCopilotOAuthClientID
	}
	if cfg.DeviceCodeURL == "" {
		cfg.DeviceCodeURL = "https://github.com/login/device/code"
	}
	if cfg.AccessTokenURL == "" {
		cfg.AccessTokenURL = "https://github.com/login/oauth/access_token"
	}
	if cfg.Scope == "" {
		cfg.Scope = "read:user"
	}
	return cfg
}

// EncodeGitHubCopilotAuthState marshals Copilot auth state for config storage.
func EncodeGitHubCopilotAuthState(state GitHubCopilotAuthState) (json.RawMessage, error) {
	if strings.TrimSpace(state.AccessToken) == "" {
		return nil, nil
	}
	if strings.TrimSpace(state.TokenType) == "" {
		state.TokenType = "bearer"
	}
	b, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// ValidateCredentials checks that provider config has usable request credentials.
func ValidateCredentials(providerID, apiKey string, raw json.RawMessage) error {
	prof, err := getProfile(providerID)
	if err != nil {
		return err
	}
	resolvedAuth, err := resolveAuth(providerID, apiKey, raw)
	if err != nil {
		return err
	}
	if prof.APIKeyRequired && resolvedAuth.APIKey == "" {
		return fmt.Errorf("%s is required for provider %q", prof.RequiredCredentialName(), providerID)
	}
	return nil
}

func resolveAuth(providerID, apiKey string, raw json.RawMessage) (ResolvedAuth, error) {
	prof, err := getProfile(providerID)
	if err != nil {
		return ResolvedAuth{}, err
	}
	if prof.authHandler == nil {
		return ResolvedAuth{APIKey: strings.TrimSpace(apiKey)}, nil
	}
	return prof.authHandler.Resolve(apiKey, raw)
}

// ResolveAuthOptions configures request-time auth resolution, including refresh.
type ResolveAuthOptions struct {
	HTTPClient         *http.Client
	GitHubCopilotOAuth GitHubCopilotOAuthConfig
	Now                time.Time
	Persist            func(apiKey string, raw json.RawMessage) error
}

func resolveAuthForRequest(ctx context.Context, providerID, apiKey string, raw json.RawMessage, opts ResolveAuthOptions) (ResolvedAuth, error) {
	if providerID != "github_copilot" {
		return resolveAuth(providerID, apiKey, raw)
	}

	state, hasState, err := decodeGitHubCopilotAuthState(raw)
	if err != nil {
		if trimmedKey := strings.TrimSpace(apiKey); trimmedKey != "" {
			return ResolvedAuth{APIKey: trimmedKey}, nil
		}
		return ResolvedAuth{}, err
	}
	if !hasState {
		return resolveAuth(providerID, apiKey, raw)
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
		if strings.TrimSpace(state.RefreshToken) == "" {
			return ResolvedAuth{}, fmt.Errorf("github_copilot token expired and cannot refresh; re-authenticate")
		}
		if !state.RefreshTokenExpiresAt.IsZero() && !state.RefreshTokenExpiresAt.After(now) {
			return ResolvedAuth{}, fmt.Errorf("github_copilot refresh token expired; re-authenticate")
		}
		refreshed, err := refreshGitHubCopilotToken(ctx, opts.HTTPClient, DefaultGitHubCopilotOAuthConfig(opts.GitHubCopilotOAuth), state.RefreshToken)
		if err != nil {
			return ResolvedAuth{}, err
		}
		state = applyGitHubCopilotTokenResponse(state, refreshed, now)
		if opts.Persist != nil {
			normalized, err := EncodeGitHubCopilotAuthState(state)
			if err != nil {
				return ResolvedAuth{}, err
			}
			if err := opts.Persist(strings.TrimSpace(state.AccessToken), normalized); err != nil {
				return ResolvedAuth{}, err
			}
		}
	}
	if strings.TrimSpace(state.AccessToken) != "" {
		return ResolvedAuth{APIKey: strings.TrimSpace(state.AccessToken)}, nil
	}
	return ResolvedAuth{APIKey: strings.TrimSpace(apiKey)}, nil
}

func gitHubCopilotAuthUpdateFromTokenResponse(resp *gitHubAccessTokenResponse, now time.Time) (*AuthUpdate, error) {
	if resp == nil || strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("github_copilot token response missing access token")
	}
	if now.IsZero() {
		now = time.Now()
	}
	state := applyGitHubCopilotTokenResponse(GitHubCopilotAuthState{}, resp, now)
	raw, err := EncodeGitHubCopilotAuthState(state)
	if err != nil {
		return nil, err
	}
	return &AuthUpdate{
		APIKey:       strings.TrimSpace(state.AccessToken),
		ProviderAuth: raw,
	}, nil
}

// NormalizeConfigAuthState canonicalizes provider-owned auth state for config persistence.
func NormalizeConfigAuthState(providerID, apiKey string, raw json.RawMessage) (json.RawMessage, error) {
	prof, err := getProfile(providerID)
	if err != nil {
		return nil, err
	}
	if prof.authHandler == nil {
		return nil, nil
	}
	return prof.authHandler.Normalize(apiKey, raw)
}

// StartGitHubCopilotDeviceFlow begins GitHub OAuth device flow.
func StartGitHubCopilotDeviceFlow(ctx context.Context, client *http.Client, cfg GitHubCopilotOAuthConfig) (*GitHubDeviceCodeResponse, error) {
	payload := map[string]string{
		"client_id": cfg.ClientID,
		"scope":     cfg.Scope,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.DeviceCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Eitri")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow unreachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub device flow start failed: HTTP %d", resp.StatusCode)
	}

	var deviceResp GitHubDeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, fmt.Errorf("GitHub device flow start failed: %v", err)
	}
	if deviceResp.Error != "" {
		return nil, fmt.Errorf("GitHub device flow start failed: %s", deviceResp.Error)
	}
	if deviceResp.DeviceCode == "" || deviceResp.UserCode == "" || deviceResp.VerificationURI == "" {
		return nil, fmt.Errorf("GitHub device flow start failed: incomplete response")
	}
	if deviceResp.Interval <= 0 {
		deviceResp.Interval = 5
	}
	if deviceResp.ExpiresIn <= 0 {
		deviceResp.ExpiresIn = 900
	}
	return &deviceResp, nil
}

// GitHubCopilotDeviceFlowPollStatus describes caller-safe device-flow poll outcome.
type GitHubCopilotDeviceFlowPollStatus string

// GitHub Copilot device-flow poll statuses exposed to API callers.
const (
	GitHubCopilotDeviceFlowAuthorized           GitHubCopilotDeviceFlowPollStatus = "authorized"
	GitHubCopilotDeviceFlowAuthorizationPending GitHubCopilotDeviceFlowPollStatus = "authorization_pending"
	GitHubCopilotDeviceFlowSlowDown             GitHubCopilotDeviceFlowPollStatus = "slow_down"
	GitHubCopilotDeviceFlowExpiredToken         GitHubCopilotDeviceFlowPollStatus = "expired_token"
	GitHubCopilotDeviceFlowAccessDenied         GitHubCopilotDeviceFlowPollStatus = "access_denied"
	GitHubCopilotDeviceFlowProviderError        GitHubCopilotDeviceFlowPollStatus = "provider_error"
)

// GitHubCopilotDeviceFlowPollResult contains normalized poll outcome data.
type GitHubCopilotDeviceFlowPollResult struct {
	Status     GitHubCopilotDeviceFlowPollStatus
	AuthUpdate *AuthUpdate
	ErrorCode  string
}

// PollGitHubCopilotDeviceFlow polls GitHub OAuth device flow and returns caller-safe status.
func PollGitHubCopilotDeviceFlow(ctx context.Context, client *http.Client, cfg GitHubCopilotOAuthConfig, deviceCode string, now time.Time) (*GitHubCopilotDeviceFlowPollResult, error) {
	payload := map[string]string{
		"client_id":   cfg.ClientID,
		"device_code": deviceCode,
		"grant_type":  GitHubDeviceFlowGrantType,
	}
	resp, err := postGitHubCopilotAccessTokenRequest(ctx, client, cfg, payload, "GitHub device flow poll failed")
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		status := GitHubCopilotDeviceFlowProviderError
		switch resp.Error {
		case "authorization_pending":
			status = GitHubCopilotDeviceFlowAuthorizationPending
		case "slow_down":
			status = GitHubCopilotDeviceFlowSlowDown
		case "expired_token", "token_expired":
			status = GitHubCopilotDeviceFlowExpiredToken
		case "access_denied":
			status = GitHubCopilotDeviceFlowAccessDenied
		}
		return &GitHubCopilotDeviceFlowPollResult{Status: status, ErrorCode: resp.Error}, nil
	}
	update, err := gitHubCopilotAuthUpdateFromTokenResponse(resp, now)
	if err != nil {
		return nil, err
	}
	return &GitHubCopilotDeviceFlowPollResult{Status: GitHubCopilotDeviceFlowAuthorized, AuthUpdate: update}, nil
}

func refreshGitHubCopilotToken(ctx context.Context, client *http.Client, cfg GitHubCopilotOAuthConfig, refreshToken string) (*gitHubAccessTokenResponse, error) {
	payload := map[string]string{
		"client_id":     cfg.ClientID,
		"refresh_token": refreshToken,
		"grant_type":    GitHubRefreshTokenGrantType,
	}
	resp, err := postGitHubCopilotAccessTokenRequest(ctx, client, cfg, payload, "GitHub token refresh failed")
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("GitHub token refresh failed: %s", resp.Error)
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return nil, fmt.Errorf("GitHub token refresh failed: incomplete response")
	}
	return resp, nil
}

func postGitHubCopilotAccessTokenRequest(ctx context.Context, client *http.Client, cfg GitHubCopilotOAuthConfig, payload map[string]string, prefix string) (*gitHubAccessTokenResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.AccessTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Eitri")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", prefix, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", prefix, resp.StatusCode)
	}

	var tokenResp gitHubAccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("%s: %v", prefix, err)
	}
	return &tokenResp, nil
}

type githubCopilotAuthHandler struct{}

func (githubCopilotAuthHandler) Normalize(apiKey string, raw json.RawMessage) (json.RawMessage, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey != "" {
		return EncodeGitHubCopilotAuthState(GitHubCopilotAuthState{
			AccessToken: trimmedKey,
			TokenType:   "bearer",
		})
	}

	state, hasState, err := decodeGitHubCopilotAuthState(raw)
	if err != nil {
		return nil, err
	}
	if hasState && strings.TrimSpace(state.AccessToken) != "" {
		return EncodeGitHubCopilotAuthState(state)
	}
	return nil, nil
}

func (githubCopilotAuthHandler) Resolve(apiKey string, raw json.RawMessage) (ResolvedAuth, error) {
	state, hasState, err := decodeGitHubCopilotAuthState(raw)
	if err != nil {
		if trimmedKey := strings.TrimSpace(apiKey); trimmedKey != "" {
			return ResolvedAuth{APIKey: trimmedKey}, nil
		}
		return ResolvedAuth{}, err
	}
	if hasState && strings.TrimSpace(state.AccessToken) != "" {
		return ResolvedAuth{APIKey: strings.TrimSpace(state.AccessToken)}, nil
	}
	return ResolvedAuth{APIKey: strings.TrimSpace(apiKey)}, nil
}

func decodeGitHubCopilotAuthState(raw json.RawMessage) (GitHubCopilotAuthState, bool, error) {
	if len(raw) == 0 {
		return GitHubCopilotAuthState{}, false, nil
	}
	var state GitHubCopilotAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		return GitHubCopilotAuthState{}, false, fmt.Errorf("invalid github_copilot provider_auth state: %w", err)
	}
	return state, true, nil
}

func applyGitHubCopilotTokenResponse(state GitHubCopilotAuthState, resp *gitHubAccessTokenResponse, now time.Time) GitHubCopilotAuthState {
	state.AccessToken = strings.TrimSpace(resp.AccessToken)
	if strings.TrimSpace(resp.TokenType) != "" {
		state.TokenType = strings.TrimSpace(resp.TokenType)
	}
	if strings.TrimSpace(resp.Scope) != "" {
		state.Scope = strings.TrimSpace(resp.Scope)
	}
	if strings.TrimSpace(resp.RefreshToken) != "" {
		state.RefreshToken = strings.TrimSpace(resp.RefreshToken)
	}
	if resp.ExpiresIn > 0 {
		state.ExpiresAt = now.Add(time.Duration(resp.ExpiresIn) * time.Second)
	} else {
		state.ExpiresAt = time.Time{}
	}
	if resp.RefreshTokenExpiresIn > 0 {
		state.RefreshTokenExpiresAt = now.Add(time.Duration(resp.RefreshTokenExpiresIn) * time.Second)
	}
	return state
}
