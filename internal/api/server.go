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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath      string          // path to config file for save
	Workspace       string          // launch workspace (process CWD)
	SessionManager  *session.Manager
	RunManager      *RunManager
	SkillsService   *skills.Service
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

	// Skills routes
	s.mux.HandleFunc("GET /skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/skills", s.handleAPISkills)
	s.mux.HandleFunc("POST /api/skills/refresh", s.handleSkillsRefresh)
	s.mux.HandleFunc("GET /api/sessions/{id}/complete/skills", s.handleCompleteSkills)
	s.mux.HandleFunc("GET /api/sessions/{id}/complete/files", s.handleCompleteFiles)
	s.mux.HandleFunc("POST /api/sessions/{id}/skills/{name}/activate", s.handleActivateSessionSkill)
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

	// Check for slash commands
	slashResult, slashErr := skills.ParseSlashInput(message, func(name string) *skills.Skill {
		return s.config.SkillsService.Lookup(name)
	})
	if slashErr != nil {
		// Unknown slash command
		if _, ok := slashErr.(*skills.UnknownCommandError); ok {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Header().Set("Content-Type", "text/html")
			component := templates.ErrorToast(slashErr.Error())
			component.Render(r.Context(), w)
			return
		}
	}

	// Activate skills from slash command
	if slashResult != nil && len(slashResult.ActivatedSkills) > 0 {
		for _, skillName := range slashResult.ActivatedSkills {
			s.config.SessionManager.ActivateSkill(id, skillName)
		}
	}

	// Remove any active skills that are no longer effective
	for _, name := range sess.ActiveSkills {
		if s.config.SkillsService.Lookup(name) == nil {
			s.config.SessionManager.DeactivateSkill(id, name)
		}
	}

	// If slash-only (no prompt), return activation event without starting a run
	if slashResult != nil && slashResult.IsSlashOnly {
		// Render the updated active skill chips
		w.Header().Set("Content-Type", "text/html")
		chips := templates.ActiveSkillChips(sess.ActiveSkills)
		chips.Render(r.Context(), w)
		return
	}

	// Determine the actual prompt to send
	prompt := message
	if slashResult != nil && slashResult.Prompt != "" {
		prompt = slashResult.Prompt
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
	err := s.config.RunManager.StartRun(r.Context(), id, prompt)
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
			text := s.config.RunManager.AppendEvent(state, w)
			if text != "" {
				s.config.SessionManager.AppendMessage(id, session.Message{
					Role:      "assistant",
					Content:   text,
					CreatedAt: time.Now(),
				})
			}
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
	if initData := mustJSON(SSEEvent{Type: "connecting"}); initData != nil {
		fmt.Fprintf(w, "data: %s\n\n", string(initData))
		flusher.Flush()
	}

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
			if data == nil {
				return
			}
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

	var toolType, toolName, toolArgs, toolOutput string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var req struct {
			Type   string          `json:"type"`
			Tool   string          `json:"tool"`
			Args   json.RawMessage `json:"args,omitempty"`
			Output string          `json:"output,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		toolType = req.Type
		toolName = req.Tool
		toolArgs = string(req.Args)
		toolOutput = req.Output
	} else {
		// Form-encoded (HTMX default)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		toolType = r.FormValue("type")
		toolName = r.FormValue("tool")
		toolArgs = r.FormValue("args")
		toolOutput = r.FormValue("output")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if toolType == "tool_call" {
		component := templates.ToolCallCard(toolName, toolArgs)
		component.Render(r.Context(), w)
	} else if toolName == "file_editor" && toolOutput != "" {
		// Parse file_editor result JSON to render FileEditCard
		var feResult struct {
			Path         string   `json:"path"`
			Mode         string   `json:"mode"`
			BytesWritten int      `json:"bytes_written"`
			OldContent   string   `json:"old_content"`
			NewContent   string   `json:"new_content"`
			DirsCreated  []string `json:"dirs_created"`
		}
		if err := json.Unmarshal([]byte(toolOutput), &feResult); err == nil {
			component := templates.FileEditCard(feResult.Path, feResult.Mode, feResult.OldContent, feResult.NewContent, feResult.BytesWritten, feResult.DirsCreated)
			component.Render(r.Context(), w)
		} else {
			// Fallback to regular tool result card
			component := templates.ToolResultCard(toolName, toolOutput)
			component.Render(r.Context(), w)
		}
	} else {
		component := templates.ToolResultCard(toolName, toolOutput)
		component.Render(r.Context(), w)
	}
}

func (s *Server) handleRenderError(w http.ResponseWriter, r *http.Request) {
	var msg string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
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
		msg = req.Message
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		msg = r.FormValue("message")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := templates.ErrorToast(msg)
	component.Render(r.Context(), w)
}

func (s *Server) handleRenderComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req struct {
		Name string                 `json:"name"`
		Data map[string]interface{} `json:"data"`
	}

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
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
	} else {
		defer r.Body.Close()
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		req.Name = r.FormValue("name")
		dataStr := r.FormValue("data")
		if dataStr != "" {
			json.Unmarshal([]byte(dataStr), &req.Data)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	switch req.Name {
	case "MermaidDiagram":
		code := ""
		if req.Data != nil {
			if c, ok := req.Data["code"].(string); ok {
				code = c
			}
		}
		component := templates.MermaidDiagram(code)
		component.Render(r.Context(), w)

	case "QuickReplies":
		var options []string
		if req.Data != nil {
			if opts, ok := req.Data["options"]; ok {
				if optsArr, ok := opts.([]interface{}); ok {
					for _, o := range optsArr {
						if s, ok := o.(string); ok {
							options = append(options, s)
						}
					}
				}
			}
		}
		component := templates.QuickReplies(id, options)
		component.Render(r.Context(), w)

	case "DiffCard":
		oldCode := ""
		newCode := ""
		lang := ""
		if req.Data != nil {
			if o, ok := req.Data["old"].(string); ok {
				oldCode = o
			}
			if n, ok := req.Data["new"].(string); ok {
				newCode = n
			}
			if l, ok := req.Data["lang"].(string); ok {
				lang = l
			}
		}
		component := templates.DiffCard(oldCode, newCode, lang)
		component.Render(r.Context(), w)

	default:
		http.Error(w, "Unknown component", http.StatusBadRequest)
	}
}

// mustJSON marshals v to JSON bytes, logging and returning nil on error.
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("json marshal error: %v", err)
		return nil
	}
	return b
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	registry := s.config.SkillsService.Registry()
	component := templates.SkillsPage(registry)
	component.Render(r.Context(), w)
}

func (s *Server) handleAPISkills(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)
	_ = browserID

	registry := s.config.SkillsService.Registry()

	// HTMX-aware: return HTML fragment when HX-Request header is present
	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	// Otherwise return JSON
	effective := registry.Effective()
	type skillJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
		Path        string `json:"path"`
	}
	result := make([]skillJSON, 0, len(effective))
	for _, s := range effective {
		result = append(result, skillJSON{
			Name:        s.Name,
			Description: s.Description,
			Scope:       string(s.Scope),
			Path:        s.Path,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"skills": result,
		"diagnostics": registry.Diagnostics(),
	})
}

func (s *Server) handleSkillsRefresh(w http.ResponseWriter, r *http.Request) {
	registry := s.config.SkillsService.Refresh()

	if r.Header.Get("HX-Request") == "true" {
		component := templates.SkillsTable(registry)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"skills": registry.Summary(),
	})
}

func (s *Server) handleCompleteSkills(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query().Get("q")
	effective := s.config.SkillsService.Effective()

	type itemJSON struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
	}
	var items []itemJSON
	for _, skill := range effective {
		if q == "" || strings.HasPrefix(skill.Name, q) {
			items = append(items, itemJSON{
				Name:        skill.Name,
				Description: skill.Description,
				Scope:       string(skill.Scope),
			})
		}
		if len(items) >= 50 {
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

func (s *Server) handleCompleteFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query().Get("q")

	// Reject ../ and absolute paths for safety
	if strings.Contains(q, "..") || strings.HasPrefix(q, "/") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	workspace := s.config.Workspace
	searchDir := workspace
	prefix := q

	// Handle subdirectory prefix: split into dir + file prefix
	if q != "" {
		if strings.HasSuffix(q, "/") {
			// q is a directory path; list its contents
			searchDir = filepath.Join(workspace, q)
			prefix = ""
		} else {
			dir := filepath.Dir(q)
			if dir != "." {
				searchDir = filepath.Join(workspace, dir)
				prefix = filepath.Base(q)
			} else {
				prefix = q
			}
		}
	}

	// Skip if searchDir doesn't exist or is outside workspace
	absSearch, err := filepath.Abs(searchDir)
	if err != nil || !strings.HasPrefix(absSearch, workspace) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	entries, err := os.ReadDir(absSearch)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	type fileItem struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	var items []fileItem

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"dist": true, "build": true, "target": true, ".cache": true,
	}

	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden unless prefix starts with dot
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}

		// Filter by prefix
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}

		if entry.IsDir() {
			if skipDirs[name] {
				continue
			}
			items = append(items, fileItem{Path: name + "/", Kind: "dir"})
		} else {
			items = append(items, fileItem{Path: name, Kind: "file"})
		}

		if len(items) >= 50 {
			break
		}
	}

	// Sort: dirs first, then lexicographic within each group
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "dir"
		}
		return items[i].Path < items[j].Path
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

func (s *Server) handleActivateSessionSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Validate skill exists and is effective
	if s.config.SkillsService.Lookup(name) == nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Header().Set("Content-Type", "text/html")
		component := templates.ErrorToast("Skill \"" + name + "\" not found or not effective")
		component.Render(r.Context(), w)
		return
	}

	// Activate in session
	activated := s.config.SessionManager.ActivateSkill(id, name)

	// Return HTMX fragment with updated chips
	if r.Header.Get("HX-Request") == "true" {
		if activated {
			// Swap the active skills section
			chips := templates.ActiveSkillChips(sess.ActiveSkills)
			chips.Render(r.Context(), w)
			return
		}
		// Already active - return current state
		chips := templates.ActiveSkillChips(sess.ActiveSkills)
		chips.Render(r.Context(), w)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
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
