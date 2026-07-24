package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

type configState struct {
	cfg    *config.Config
	models []string
	err    error
}

func (cs configState) valid() bool {
	return cs.cfg != nil && cs.err == nil
}

func (s *Server) loadConfigState(ctx context.Context) configState {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		return configState{err: err}
	}
	if err := config.Validate(cfg); err != nil {
		return configState{cfg: cfg, err: err}
	}
	models, err := s.fetchModelList(ctx, cfg)
	if err != nil {
		return configState{cfg: cfg, err: err}
	}
	if err := config.ValidateSelectedModel(cfg, models); err != nil {
		return configState{cfg: cfg, models: models, err: err}
	}
	return configState{cfg: cfg, models: models}
}

func maskedConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg
	if masked.APIKey != "" {
		masked.APIKey = config.MaskAPIKey(masked.APIKey)
	}
	masked.ProviderAuth = nil
	return &masked
}

func writeSettingsForm(w http.ResponseWriter, r *http.Request, status int, cfg *config.Config, models []string, message string) {
	writeSettingsFormWithState(w, r, status, cfg, models, message, "", nil)
}

func writeConfigError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if isHTMXRequest(r) || !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = templates.ErrorToast(message).Render(r.Context(), w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	state := s.loadConfigState(r.Context())
	if state.cfg == nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	contextWindow := state.cfg.ContextWindowTokens
	if contextWindow == 0 {
		contextWindow = 256000
	}
	component := templates.SettingsView(state.cfg, state.models, s.config.Workspace, s.chatPathForRequest(r), r.URL.Path, contextWindow)
	component.Render(r.Context(), w)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	state := s.loadConfigState(r.Context())
	models := state.models
	if state.cfg != nil {
		cfg = state.cfg
	}

	maskedCfg := maskedConfig(cfg)

	// HTMX-aware: return HTML fragment when HX-Request header is present
	if isHTMXRequest(r) {
		component := templates.SettingsForm(maskedCfg, models, "", "", nil, "")
		component.Render(r.Context(), w)
		return
	}

	// Otherwise return JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskedCfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	patch, err := parseConfigPatch(r)
	if err != nil {
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Load current config
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	newCfg := normalizePatchedConfig(cfg, patch)

	// Validate field-level constraints
	if err := config.Validate(newCfg); err != nil {
		if isHTMXRequest(r) {
			writeSettingsForm(w, r, http.StatusOK, newCfg, nil, err.Error())
			return
		}
		writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Validate provider credentials by calling the profile's model discovery path.
	models, err := s.fetchModelList(r.Context(), newCfg)
	if err != nil {
		if isHTMXRequest(r) {
			writeSettingsForm(w, r, http.StatusOK, newCfg, nil, err.Error())
			return
		}
		writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if strings.TrimSpace(newCfg.Model) != "" {
		if err := config.ValidateSelectedModel(newCfg, models); err != nil {
			if isHTMXRequest(r) {
				writeSettingsForm(w, r, http.StatusOK, newCfg, models, err.Error())
				return
			}
			writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}

	// Save
	if err := s.saveProviderConfig(newCfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Render form with success indicator
	component := templates.SettingsForm(maskedConfig(newCfg), models, "", "", nil, "✓ Saved")
	component.Render(r.Context(), w)
}

// discoverModelList calls Provider discovery seam. It passes PersistAuth if
// available so that refreshed auth state is persisted automatically.
func (s *Server) discoverModelList(ctx context.Context, cfg *config.Config) (*provider.DiscoveryResult, error) {
	return provider.DiscoverModels(ctx, provider.DiscoveryRequest{
		ProviderID:   cfg.Provider,
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		ProviderAuth: cfg.ProviderAuth,
	}, provider.DiscoveryOptions{
		HTTPClient:         s.httpClient,
		GitHubCopilotOAuth: s.copilotOAuth,
		PersistAuth:        s.persistAuth(),
	})
}

// fetchModelList calls Provider discovery seam. Auth refresh is persisted
// automatically via PersistAuth when configured, or manually when not.
func (s *Server) fetchModelList(ctx context.Context, cfg *config.Config) ([]string, error) {
	result, err := s.discoverModelList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if result.AuthUpdate != nil {
		// PersistAuth was not set — save the auth update manually.
		applyAuthUpdate(cfg, result.AuthUpdate)
		if err := s.saveProviderConfig(cfg); err != nil {
			return nil, fmt.Errorf("failed to save refreshed provider auth: %w", err)
		}
	} else if s.persistAuth() != nil {
		// PersistAuth handled persistence; reload only ProviderAuth (refreshed by
		// auth handler). Do NOT overwrite APIKey — caller may have set a new key.
		loaded, loadErr := config.Load(s.config.ConfigPath)
		if loadErr == nil {
			cfg.ProviderAuth = append(json.RawMessage(nil), loaded.ProviderAuth...)
		}
	}
	return result.Models, nil
}

func applyAuthUpdate(cfg *config.Config, update *provider.AuthUpdate) {
	if update == nil {
		return
	}
	cfg.APIKey = update.APIKey
	if len(update.ProviderAuth) == 0 {
		cfg.ProviderAuth = nil
		return
	}
	cfg.ProviderAuth = append(json.RawMessage(nil), update.ProviderAuth...)
}

// persistAuth returns the PersistAuth callback, or nil if none is configured.
func (s *Server) persistAuth() provider.PersistAuthFunc {
	return s.persistAuthFn
}

func (s *Server) saveProviderConfig(cfg *config.Config) error {
	return config.Save(s.config.ConfigPath, cfg)
}

func (s *Server) handleGetModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	models, err := s.fetchModelList(r.Context(), cfg)
	if err != nil {
		if isHTMXRequest(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_ = templates.TestConnectionResult(false, "Connection failed: "+err.Error()).Render(r.Context(), w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = templates.TestConnectionResult(true, "Connection OK").Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}
