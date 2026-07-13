package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath string // path to config file for save
	// Add fields as features are implemented
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config ServerConfig
	mux    *http.ServeMux
	httpClient *http.Client // shared client with timeout for outbound requests
}

// NewServer creates a new Server with routes registered.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		config: cfg,
		mux:    http.NewServeMux(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	s.registerRoutes()
	return s
}

// Handler returns the HTTP handler for use with httptest or http.Server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.Handle("GET /static/", http.FileServerFS(assets.Files))

	// Root serves the base HTML page
	s.mux.HandleFunc("GET /{$}", s.handleRoot)

	// Settings
	s.mux.HandleFunc("GET /settings", s.handleSettings)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	s.mux.HandleFunc("GET /api/models", s.handleGetModels)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	component := templates.Base("Eitri — AI Assistant")
	component.Render(r.Context(), w)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	component := templates.SettingsView(cfg)
	component.Render(r.Context(), w)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Mask the API key for GET responses
	maskedCfg := *cfg
	if maskedCfg.APIKey != "" {
		maskedCfg.APIKey = config.MaskAPIKey(maskedCfg.APIKey)
	}

	// HTMX-aware: return HTML fragment when HX-Request header is present
	if r.Header.Get("HX-Request") == "true" {
		component := templates.SettingsForm(&maskedCfg, nil)
		component.Render(r.Context(), w)
		return
	}

	// Otherwise return JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskedCfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var patch map[string]interface{}

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &patch); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// URL-encoded form data (HTMX default for form submissions)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}
		patch = make(map[string]interface{}, len(r.Form))
		for k, v := range r.Form {
			patch[k] = v[0]
		}
		// Preserve existing API key when form field is empty and clear is not requested
		if v, ok := patch["api_key"]; ok && v.(string) == "" {
			if _, hasClear := patch["clear_api_key"]; !hasClear {
				delete(patch, "api_key")
			}
		}
	}

	// Load current config
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	// Apply patch
	newCfg := config.Merge(cfg, patch)

	// Convert timeout fields from seconds (form) to nanoseconds (config storage)
	if _, ok := patch["session_timeout"]; ok {
		// If value is in seconds range (well below 1e9), multiply to ns
		if newCfg.SessionTimeout < 1_000_000_000 && newCfg.SessionTimeout > 0 {
			newCfg.SessionTimeout *= 1_000_000_000
		}
	}
	if _, ok := patch["command_timeout"]; ok {
		if newCfg.CommandTimeout < 1_000_000_000 && newCfg.CommandTimeout > 0 {
			newCfg.CommandTimeout *= 1_000_000_000
		}
	}

	// Validate field-level constraints
	if err := config.Validate(newCfg); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Validate provider credentials by calling /v1/models and get model list
	models, err := s.fetchModelList(newCfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Save
	if err := config.Save(s.config.ConfigPath, newCfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Render form with models populated
	maskedCfg := *newCfg
	if maskedCfg.APIKey != "" {
		maskedCfg.APIKey = config.MaskAPIKey(maskedCfg.APIKey)
	}
	component := templates.SettingsForm(&maskedCfg, models)
	component.Render(r.Context(), w)
}

// fetchModelList calls /v1/models on the provider and returns the list of model IDs.
// Also validates that the provider credentials work.
func (s *Server) fetchModelList(cfg *config.Config) ([]string, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	if cfg.Provider == "opencode_go" && cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required for provider %q", cfg.Provider)
	}

	modelsURL, err := url.JoinPath(cfg.BaseURL, "/v1/models")
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Model discovery failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Model discovery failed: provider returned HTTP %d", resp.StatusCode)
	}

	// Parse OpenAI-compatible /v1/models response
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse model list: %v", err)
	}

	modelIDs := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		if m.ID != "" {
			modelIDs = append(modelIDs, m.ID)
		}
	}
	return modelIDs, nil
}

func (s *Server) handleGetModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	models, err := s.fetchModelList(cfg)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}
