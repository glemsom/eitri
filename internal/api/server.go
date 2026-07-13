package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath      string          // path to config file for save
	Workspace       string          // launch workspace (process CWD)
	SessionManager  *session.Manager
	RunManager      *RunManager
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config     ServerConfig
	mux        *http.ServeMux
	httpClient *http.Client
	sses       sync.Map // sessionID -> chan SSEEvent (active stream connections)
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

	// Root serves the base HTML page — redirects to session if browser known
	s.mux.HandleFunc("GET /{$}", s.handleRoot)

	// Sessions
	s.mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)

	// Settings
	s.mux.HandleFunc("GET /settings", s.handleSettings)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handlePutConfig)
	s.mux.HandleFunc("GET /api/models", s.handleGetModels)

	// Agent run + SSE streaming (issue #6)
	s.mux.HandleFunc("POST /api/sessions/{id}/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleStream)
	s.mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/sessions/{id}/render/markdown", s.handleRenderMarkdown)
	s.mux.HandleFunc("POST /api/sessions/{id}/render/tool-card", s.handleRenderToolCard)
	s.mux.HandleFunc("POST /api/sessions/{id}/render/error", s.handleRenderError)
	s.mux.HandleFunc("POST /api/sessions/{id}/render/component", s.handleRenderComponent)

	// Skills (stub - full implementation in issue #7)
	s.mux.HandleFunc("GET /skills", s.handleSkillsStub)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	browserID := s.ensureBrowserID(w, r)

	// Try last active session
	if last := s.config.SessionManager.LastActive(browserID); last != nil {
		http.Redirect(w, r, "/sessions/"+last.ID, http.StatusFound)
		return
	}

	// Try any session for browser
	sessions := s.config.SessionManager.ListByBrowser(browserID)
	if len(sessions) > 0 {
		http.Redirect(w, r, "/sessions/"+sessions[0].ID, http.StatusFound)
		return
	}

	// Create first session
	sess, err := s.config.SessionManager.Create(browserID)
	if err != nil {
		// Global cap: return error page
		w.WriteHeader(http.StatusTooManyRequests)
		component := templates.ErrorToast("Session cap reached")
		component.Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, "/sessions/"+sess.ID, http.StatusFound)
}

// ensureBrowserID returns the browser_id cookie value, creating one if missing.
func (s *Server) ensureBrowserID(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie("browser_id")
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// Generate new browser ID
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("failed to generate browser ID: %v", err)
		// Fallback: use a timestamp-based ID
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	id := fmt.Sprintf("%x", b)

	http.SetCookie(w, &http.Cookie{
		Name:     "browser_id",
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   0, // session cookie
	})

	return id
}

// browserIDFromRequest returns the browser_id from cookie, or empty string if missing.
func (s *Server) browserIDFromRequest(r *http.Request) string {
	cookie, err := r.Cookie("browser_id")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// hxRedirect sends an HX-Redirect header for HTMX requests, or a standard HTTP redirect.
func (s *Server) hxRedirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusFound)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	browserID := s.ensureBrowserID(w, r)

	sess, err := s.config.SessionManager.Create(browserID)
	if err != nil {
		w.WriteHeader(http.StatusTooManyRequests)
		component := templates.ErrorToast(err.Error())
		component.Render(r.Context(), w)
		return
	}

	s.hxRedirect(w, r, "/sessions/"+sess.ID)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	// Ensure browser_id exists
	if browserID == "" {
		browserID = s.ensureBrowserID(w, r)
	}

	sess := s.config.SessionManager.Get(id)

	// Check config validity for setup banner
	cfg, err := config.Load(s.config.ConfigPath)
	configValid := err == nil && cfg != nil && cfg.Model != "" && cfg.BaseURL != ""
	if cfg != nil && cfg.Provider == "opencode_go" && cfg.APIKey == "" {
		configValid = false
	}

	// Stale session (id doesn't exist at all) → redirect to /
	if sess == nil {
		s.hxRedirect(w, r, "/")
		return
	}

	// Ownership mismatch → 404
	if sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	sessions := s.config.SessionManager.ListByBrowser(browserID)

	component := templates.ChatPage(sessions, id, sess, s.config.Workspace, configValid)
	component.Render(r.Context(), w)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	if browserID == "" {
		http.Error(w, "No browser ID", http.StatusUnauthorized)
		return
	}

	sess := s.config.SessionManager.Get(id)
	if sess == nil {
		// Session already gone — redirect
		s.hxRedirect(w, r, "/")
		return
	}
	if sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	s.config.SessionManager.Delete(id)

	// Redirect to next available session or root
	sessions := s.config.SessionManager.ListByBrowser(browserID)
	if len(sessions) > 0 {
		s.hxRedirect(w, r, "/sessions/"+sessions[0].ID)
		return
	}

	// No sessions left, create one
	newSess, err := s.config.SessionManager.Create(browserID)
	if err != nil {
		w.WriteHeader(http.StatusTooManyRequests)
		component := templates.ErrorToast(err.Error())
		component.Render(r.Context(), w)
		return
	}

	s.hxRedirect(w, r, "/sessions/"+newSess.ID)
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

	// Invalidate runner cache on config change
	if s.config.RunManager != nil {
		s.config.RunManager.runnerMgr.Invalidate()
		s.config.RunManager.UpdateProviderConfig(newCfg)
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

// ————— Chat + SSE run handlers (issue #6) —————

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Parse message
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	message := r.FormValue("message")
	if message == "" {
		http.Error(w, "Message required", http.StatusBadRequest)
		return
	}

	// Check for active run (concurrent run protection)
	if s.config.RunManager.ActiveRun(id) != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusConflict)
		component := templates.ErrorToast("This session already has an active run. Wait for it to complete or cancel it.")
		component.Render(r.Context(), w)
		return
	}

	// Append user message to session
	s.config.SessionManager.AppendMessage(id, session.Message{
		Role:    "user",
		Content: message,
		CreatedAt: time.Now(),
	})

	// Start run in background
	err := s.config.RunManager.StartRun(r.Context(), id, message)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		component := templates.ErrorToast(err.Error())
		component.Render(r.Context(), w)
		return
	}

	// Update session status
	s.config.SessionManager.UpdateStatus(id, session.StatusRunning)

	// Start SSE event processing in background
	state := s.config.RunManager.ActiveRun(id)
	if state != nil {
		sseCh := make(chan SSEEvent, 100)
		s.sses.Store(id, sseCh)

		go func() {
			defer func() {
				s.sses.Delete(id)
				close(sseCh)
				s.config.SessionManager.UpdateStatus(id, session.StatusIdle)
			}()

			w := NewSSEWriter(sseCh)
			s.config.RunManager.AppendEvent(state, w)
		}()
	}

	// Render user bubble + OOB input state
	// Return HTMX response with HX-Trigger for SSE connect
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"eitri:connectRunStream":"`+id+`"}`)

	userBubble := templates.UserBubble(message)
	userBubble.Render(r.Context(), w)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Get the SSE channel for this session
	raw, ok := s.sses.Load(id)
	if !ok {
		http.Error(w, "No active run for this session", http.StatusNotFound)
		return
	}
	sseCh, ok := raw.(chan SSEEvent)
	if !ok {
		http.Error(w, "Stream error", http.StatusInternalServerError)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial connecting event
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(SSEEvent{Type: "connecting"}))
	flusher.Flush()

	ctx := r.Context()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case evt, ok := <-sseCh:
			if !ok {
				return
			}
			data := mustJSON(evt)
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()

		case <-keepAlive.C:
			// SSE keep-alive comment
			fmt.Fprintf(w, ":keepalive\n\n")
			flusher.Flush()

		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	s.config.RunManager.CancelRun(id)
	s.config.SessionManager.UpdateStatus(id, session.StatusIdle)

	// Return re-enabled input
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := templates.MessageInput(id, false)
	component.Render(r.Context(), w)
}

func (s *Server) handleRenderMarkdown(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req struct {
		MessageID string `json:"message_id"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Look up the last assistant message in session
	// In v1, we render the accumulated run content from the session messages
	var content string
	sess = s.config.SessionManager.Get(id)
	if sess != nil {
		for i := len(sess.Messages) - 1; i >= 0; i-- {
			if sess.Messages[i].Role == "assistant" {
				content = sess.Messages[i].Content
				break
			}
		}
	}

	html := renderMarkdownToHTML(content)
	component := templates.AssistantBubble(html)
	component.Render(r.Context(), w)
}

func (s *Server) handleRenderToolCard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		Type string          `json:"type"`
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args,omitempty"`
		Output string        `json:"output,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Type == "tool_call" {
		component := templates.ToolCallCard(req.Tool, string(req.Args))
		component.Render(r.Context(), w)
	} else {
		component := templates.ToolResultCard(req.Tool, req.Output)
		component.Render(r.Context(), w)
	}
}

func (s *Server) handleRenderError(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)
	if browserID == "" {
		// Allow anonymous error rendering
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	component := templates.ErrorToast(req.Message)
	component.Render(r.Context(), w)
}

func (s *Server) handleRenderComponent(w http.ResponseWriter, r *http.Request) {
	// Stub - full generative UI implementation in issue #8
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "<!-- component rendering not yet implemented -->")
}

// mustJSON marshals v to JSON bytes, panicking on error.
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("json marshal: " + err.Error())
	}
	return b
}

func (s *Server) handleSkillsStub(w http.ResponseWriter, r *http.Request) {
	component := templates.Base("Eitri — Skills")
	component.Render(r.Context(), w)
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
