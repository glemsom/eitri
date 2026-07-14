package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
)

const githubDeviceFlowGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// GitHubCopilotOAuthConfig configures GitHub OAuth device-flow endpoints.
type GitHubCopilotOAuthConfig struct {
	ClientID       string
	DeviceCodeURL  string
	AccessTokenURL string
	Scope          string
}

func defaultGitHubCopilotOAuthConfig(cfg GitHubCopilotOAuthConfig) GitHubCopilotOAuthConfig {
	if cfg.ClientID == "" {
		cfg.ClientID = os.Getenv("EITRI_GITHUB_CLIENT_ID")
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

type copilotDeviceFlowStore struct {
	mu    sync.Mutex
	flows map[string]*copilotDeviceFlowState
}

type copilotDeviceFlowState struct {
	ID              string
	BrowserID       string
	Config          *config.Config
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	ExpiresAt       time.Time
	NextPollAt      time.Time
}

func newCopilotDeviceFlowStore() *copilotDeviceFlowStore {
	return &copilotDeviceFlowStore{flows: map[string]*copilotDeviceFlowState{}}
}

func (s *copilotDeviceFlowStore) put(flow *copilotDeviceFlowState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[flow.ID] = flow
}

func (s *copilotDeviceFlowStore) get(id string) (*copilotDeviceFlowState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.flows[id]
	if !ok {
		return nil, false
	}
	clone := *flow
	if flow.Config != nil {
		cfgCopy := *flow.Config
		clone.Config = &cfgCopy
	}
	return &clone, true
}

func (s *copilotDeviceFlowStore) update(flow *copilotDeviceFlowState) {
	s.put(flow)
}

func (s *copilotDeviceFlowStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.flows, id)
}

type githubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
}

type githubAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

func parseConfigPatch(r *http.Request) (map[string]interface{}, error) {
	var patch map[string]interface{}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		defer r.Body.Close()
		if err := json.Unmarshal(body, &patch); err != nil {
			return nil, fmt.Errorf("invalid JSON")
		}
		return patch, nil
	}

	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	patch = make(map[string]interface{}, len(r.Form))
	for k, v := range r.Form {
		patch[k] = v[0]
	}
	if v, ok := patch["api_key"]; ok && v.(string) == "" {
		if _, hasClear := patch["clear_api_key"]; !hasClear {
			delete(patch, "api_key")
		}
	}
	return patch, nil
}

func normalizePatchedConfig(cfg *config.Config, patch map[string]interface{}) *config.Config {
	if v, ok := patch["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" && s == config.MaskAPIKey(cfg.APIKey) {
			delete(patch, "api_key")
		}
	}
	newCfg := config.Merge(cfg, patch)
	if _, ok := patch["session_timeout"]; ok {
		if newCfg.SessionTimeout < 1_000_000_000 && newCfg.SessionTimeout > 0 {
			newCfg.SessionTimeout *= 1_000_000_000
		}
	}
	if _, ok := patch["command_timeout"]; ok {
		if newCfg.CommandTimeout < 1_000_000_000 && newCfg.CommandTimeout > 0 {
			newCfg.CommandTimeout *= 1_000_000_000
		}
	}
	return newCfg
}

func validateForCopilotDeviceFlow(cfg *config.Config) error {
	validateCfg := *cfg
	if validateCfg.Provider == "github_copilot" && strings.TrimSpace(validateCfg.APIKey) == "" {
		validateCfg.APIKey = "device-flow-pending"
	}
	return config.Validate(&validateCfg)
}

func writeSettingsFormWithState(w http.ResponseWriter, r *http.Request, status int, cfg *config.Config, models []string, errorMessage string, noticeMessage string, deviceFlow *templates.CopilotDeviceFlowView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = templates.SettingsForm(cfg, models, errorMessage, noticeMessage, deviceFlow).Render(r.Context(), w)
}

func (s *Server) startCopilotDeviceFlow(ctx context.Context) (*githubDeviceCodeResponse, error) {
	payload := map[string]string{
		"client_id": s.copilotOAuth.ClientID,
		"scope":     s.copilotOAuth.Scope,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.copilotOAuth.DeviceCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Eitri")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow unreachable: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub device flow start failed: HTTP %d", resp.StatusCode)
	}

	var deviceResp githubDeviceCodeResponse
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

func (s *Server) pollCopilotDeviceFlow(ctx context.Context, flow *copilotDeviceFlowState) (*githubAccessTokenResponse, error) {
	payload := map[string]string{
		"client_id":   s.copilotOAuth.ClientID,
		"device_code": flow.DeviceCode,
		"grant_type":  githubDeviceFlowGrantType,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.copilotOAuth.AccessTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Eitri")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub device flow poll failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub device flow poll failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp githubAccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("GitHub device flow poll failed: %v", err)
	}
	return &tokenResp, nil
}

func (s *Server) copilotDeviceFlowView(flow *copilotDeviceFlowState) *templates.CopilotDeviceFlowView {
	if flow == nil {
		return nil
	}
	seconds := int(flow.Interval / time.Second)
	if seconds <= 0 {
		seconds = 5
	}
	return &templates.CopilotDeviceFlowView{
		ID:              flow.ID,
		UserCode:        flow.UserCode,
		VerificationURI: flow.VerificationURI,
		PollURL:         "/api/providers/github_copilot/device-flow/" + flow.ID,
		PollTrigger:     fmt.Sprintf("every %ds", seconds),
		CancelURL:       "/api/providers/github_copilot/device-flow/" + flow.ID,
	}
}

func newCopilotDeviceFlowID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("flow-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

func (s *Server) handleStartCopilotDeviceFlow(w http.ResponseWriter, r *http.Request) {
	if s.copilotOAuth.ClientID == "" {
		cfg, err := config.Load(s.config.ConfigPath)
		if err != nil || cfg == nil {
			fallback := config.Defaults()
			cfg = &fallback
		}
		writeSettingsFormWithState(w, r, http.StatusOK, cfg, nil, "GitHub Copilot OAuth not configured. Set EITRI_GITHUB_CLIENT_ID and retry.", "", nil)
		return
	}

	patch, err := parseConfigPatch(r)
	if err != nil {
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}
	newCfg := normalizePatchedConfig(cfg, patch)
	if newCfg.Provider != "github_copilot" {
		writeSettingsFormWithState(w, r, http.StatusOK, newCfg, nil, "Select GitHub Copilot before starting GitHub auth.", "", nil)
		return
	}
	if err := validateForCopilotDeviceFlow(newCfg); err != nil {
		writeSettingsFormWithState(w, r, http.StatusOK, newCfg, nil, err.Error(), "", nil)
		return
	}

	deviceResp, err := s.startCopilotDeviceFlow(r.Context())
	if err != nil {
		writeSettingsFormWithState(w, r, http.StatusOK, newCfg, nil, err.Error(), "", nil)
		return
	}

	browserID := s.ensureBrowserID(w, r)
	flow := &copilotDeviceFlowState{
		ID:              newCopilotDeviceFlowID(),
		BrowserID:       browserID,
		Config:          newCfg,
		DeviceCode:      deviceResp.DeviceCode,
		UserCode:        deviceResp.UserCode,
		VerificationURI: deviceResp.VerificationURI,
		Interval:        time.Duration(deviceResp.Interval) * time.Second,
		ExpiresAt:       time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second),
		NextPollAt:      time.Now(),
	}
	s.copilotFlows.put(flow)
	writeSettingsFormWithState(w, r, http.StatusOK, newCfg, nil, "", "", s.copilotDeviceFlowView(flow))
}

func (s *Server) handlePollCopilotDeviceFlow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flow, ok := s.copilotFlows.get(id)
	if !ok || flow.BrowserID != s.browserIDFromRequest(r) {
		http.NotFound(w, r)
		return
	}
	if time.Now().After(flow.ExpiresAt) {
		s.copilotFlows.delete(id)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "GitHub device flow expired. Start again.", "", nil)
		return
	}
	if time.Now().Before(flow.NextPollAt) {
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "", "", s.copilotDeviceFlowView(flow))
		return
	}

	resp, err := s.pollCopilotDeviceFlow(r.Context(), flow)
	if err != nil {
		s.copilotFlows.delete(id)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, err.Error(), "", nil)
		return
	}

	switch resp.Error {
	case "":
		// continue below
	case "authorization_pending":
		flow.NextPollAt = time.Now().Add(flow.Interval)
		s.copilotFlows.update(flow)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "", "", s.copilotDeviceFlowView(flow))
		return
	case "slow_down":
		flow.Interval += 5 * time.Second
		flow.NextPollAt = time.Now().Add(flow.Interval)
		s.copilotFlows.update(flow)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "", "", s.copilotDeviceFlowView(flow))
		return
	case "expired_token":
		s.copilotFlows.delete(id)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "GitHub device flow expired. Start again.", "", nil)
		return
	case "access_denied":
		s.copilotFlows.delete(id)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "GitHub device flow was denied. Start again when ready.", "", nil)
		return
	default:
		s.copilotFlows.delete(id)
		writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "GitHub device flow failed: "+resp.Error, "", nil)
		return
	}

	newCfg := *flow.Config
	newCfg.APIKey = resp.AccessToken
	if err := config.Save(s.config.ConfigPath, &newCfg); err != nil {
		s.copilotFlows.delete(id)
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.config.RunManager != nil {
		s.config.RunManager.runnerMgr.Invalidate()
		s.config.RunManager.UpdateProviderConfig(&newCfg)
	}

	models, modelsErr := s.fetchModelList(r.Context(), &newCfg)
	s.copilotFlows.delete(id)
	if modelsErr != nil {
		writeSettingsFormWithState(w, r, http.StatusOK, maskedConfig(&newCfg), nil, modelsErr.Error(), "GitHub Copilot connected. Token saved.", nil)
		return
	}
	writeSettingsFormWithState(w, r, http.StatusOK, maskedConfig(&newCfg), models, "", "GitHub Copilot connected. Models refreshed.", nil)
}

func (s *Server) handleCancelCopilotDeviceFlow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flow, ok := s.copilotFlows.get(id)
	if !ok || flow.BrowserID != s.browserIDFromRequest(r) {
		http.NotFound(w, r)
		return
	}
	s.copilotFlows.delete(id)
	writeSettingsFormWithState(w, r, http.StatusOK, flow.Config, nil, "", "GitHub authentication canceled.", nil)
}
