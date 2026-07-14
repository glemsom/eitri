package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const GitHubDeviceFlowGrantType = "urn:ietf:params:oauth:grant-type:device_code"
const DefaultGitHubCopilotOAuthClientID = "Iv1.b507a08c87ecfe98"

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
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type,omitempty"`
	Scope       string `json:"scope,omitempty"`
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

// GitHubAccessTokenResponse is GitHub device-flow poll response.
type GitHubAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
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

// ResolveAuth resolves provider-owned auth state into request credentials.
func ResolveAuth(providerID, apiKey string, raw json.RawMessage) (ResolvedAuth, error) {
	prof, err := Get(providerID)
	if err != nil {
		return ResolvedAuth{}, err
	}
	if prof.authHandler == nil {
		return ResolvedAuth{APIKey: strings.TrimSpace(apiKey)}, nil
	}
	return prof.authHandler.Resolve(apiKey, raw)
}

// NormalizeAuthState canonicalizes provider-owned auth state for config persistence.
func NormalizeAuthState(providerID, apiKey string, raw json.RawMessage) (json.RawMessage, error) {
	prof, err := Get(providerID)
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

// PollGitHubCopilotDeviceFlow polls GitHub OAuth device flow for access token.
func PollGitHubCopilotDeviceFlow(ctx context.Context, client *http.Client, cfg GitHubCopilotOAuthConfig, deviceCode string) (*GitHubAccessTokenResponse, error) {
	payload := map[string]string{
		"client_id":   cfg.ClientID,
		"device_code": deviceCode,
		"grant_type":  GitHubDeviceFlowGrantType,
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
		return nil, fmt.Errorf("GitHub device flow poll failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub device flow poll failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp GitHubAccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("GitHub device flow poll failed: %v", err)
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
