package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

type GitHubCopilotOAuthConfig = provider.GitHubCopilotOAuthConfig

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

func (s *Server) startCopilotDeviceFlow(ctx context.Context) (*provider.GitHubDeviceCodeResponse, error) {
	return provider.StartGitHubCopilotDeviceFlow(ctx, s.httpClient, s.copilotOAuth)
}

func (s *Server) pollCopilotDeviceFlow(ctx context.Context, flow *copilotDeviceFlowState) (*provider.GitHubAccessTokenResponse, error) {
	return provider.PollGitHubCopilotDeviceFlow(ctx, s.httpClient, s.copilotOAuth, flow.DeviceCode)
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
		writeSettingsFormWithState(w, r, http.StatusOK, cfg, nil, "GitHub Copilot OAuth not configured. Retry or check server configuration.", "", nil)
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
	initialAuthUpdate, err := provider.GitHubCopilotAuthUpdateFromTokenResponse(resp, time.Now())
	if err != nil {
		s.copilotFlows.delete(id)
		http.Error(w, "Failed to store provider auth: "+err.Error(), http.StatusInternalServerError)
		return
	}
	applyAuthUpdate(&newCfg, initialAuthUpdate)

	discoveryResult, modelsErr := s.discoverModelList(r.Context(), &newCfg)
	if modelsErr == nil {
		applyAuthUpdate(&newCfg, discoveryResult.AuthUpdate)
	}
	if err := s.saveProviderConfig(&newCfg); err != nil {
		s.copilotFlows.delete(id)
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.copilotFlows.delete(id)
	if modelsErr != nil {
		writeSettingsFormWithState(w, r, http.StatusOK, maskedConfig(&newCfg), nil, modelsErr.Error(), "GitHub Copilot connected. Token saved.", nil)
		return
	}
	writeSettingsFormWithState(w, r, http.StatusOK, maskedConfig(&newCfg), discoveryResult.Models, "", "GitHub Copilot connected. Models refreshed.", nil)
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
