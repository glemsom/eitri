package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath     string // path to config file for save
	Workspace      string // launch workspace (process CWD)
	SessionManager *session.Manager
	RunManager     *RunManager
	SkillsService  *skills.Service
	Logger         *slog.Logger
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config     ServerConfig
	mux        *http.ServeMux
	httpClient *http.Client
	logger     *slog.Logger
}

const maxRequestBodyBytes = 1 << 20

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rw *responseRecorder) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseRecorder) Write(p []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(p)
}

func (rw *responseRecorder) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

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

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func maskedConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg
	if masked.APIKey != "" {
		masked.APIKey = config.MaskAPIKey(masked.APIKey)
	}
	return &masked
}

func writeSettingsForm(w http.ResponseWriter, r *http.Request, status int, cfg *config.Config, models []string, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = templates.SettingsForm(cfg, models, message).Render(r.Context(), w)
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

func (s *Server) notifySessionClosed(sessionID, message string) {
	if s.config.RunManager == nil {
		return
	}
	s.config.RunManager.notifySessionClosed(sessionID, message)
}

// CloseActiveStreams notifies all attached SSE clients that their stream is closing.
func (s *Server) CloseActiveStreams(message string) {
	if s.config.RunManager == nil {
		return
	}
	s.config.RunManager.notifyAllStreamsClosed(message)
}

// NewServer creates a new Server with routes registered.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		config: cfg,
		mux:    http.NewServeMux(),
		logger: logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	s.registerRoutes()
	return s
}

// Handler returns the HTTP handler for use with httptest or http.Server.
func (s *Server) Handler() http.Handler {
	return s.withMiddleware(s.mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return s.requestLoggingMiddleware(s.requestBodyLimitMiddleware(next))
}

func (s *Server) requestBodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if r.ContentLength > maxRequestBodyBytes {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		s.logger.Info("http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("session_id", extractSessionIDFromPath(r.URL.Path)),
		)
	})
}

func extractSessionIDFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "sessions" {
		return parts[1]
	}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "sessions" {
		return parts[2]
	}
	return ""
}

func isRequestTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

func writeRequestTooLarge(w http.ResponseWriter) {
	http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
}

func renderSessionForPage(sess *session.UISession) *session.UISession {
	if sess == nil {
		return nil
	}

	rendered := *sess
	rendered.ActiveSkills = append([]string(nil), sess.ActiveSkills...)
	rendered.Messages = make([]session.Message, len(sess.Messages))
	for i, msg := range sess.Messages {
		rendered.Messages[i] = msg
		if msg.Role == "assistant" {
			rendered.Messages[i].Content = renderMarkdownToHTML(msg.Content)
		}
	}
	return &rendered
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(assets.Files)))

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
		s.logger.Warn("failed to generate browser ID", slog.Any("error", err))
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

func (s *Server) chatPathForRequest(r *http.Request) string {
	browserID := s.browserIDFromRequest(r)
	if browserID == "" {
		return "/"
	}
	if last := s.config.SessionManager.LastActive(browserID); last != nil {
		return "/sessions/" + last.ID
	}
	sessions := s.config.SessionManager.ListByBrowser(browserID)
	if len(sessions) > 0 {
		return "/sessions/" + sessions[0].ID
	}
	return "/"
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

	state := s.loadConfigState(r.Context())
	configValid := state.valid()

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
	renderedSession := renderSessionForPage(sess)

	component := templates.ChatPage(sessions, id, renderedSession, s.config.Workspace, configValid)
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

	s.notifySessionClosed(id, "Session closed")
	if s.config.RunManager != nil {
		if err := s.config.RunManager.CloseSession(id); err != nil {
			http.Error(w, "Failed to close session executor", http.StatusInternalServerError)
			return
		}
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
	state := s.loadConfigState(r.Context())
	if state.cfg == nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	component := templates.SettingsView(state.cfg, state.models, s.config.Workspace, s.chatPathForRequest(r))
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
		component := templates.SettingsForm(maskedCfg, models, "")
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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

	// Detect masked-key round-trip: when the submitted api_key matches
	// the masked version of the current key, preserve the real key.
	// Masked keys (sk-pd...AWi) come from the settings form's value attr
	// and would overwrite the real key if saved as-is.
	if v, ok := patch["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" && s == config.MaskAPIKey(cfg.APIKey) {
			delete(patch, "api_key")
		}
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
	component := templates.SettingsForm(maskedConfig(newCfg), models, "")
	component.Render(r.Context(), w)
}

// fetchModelList calls the provider profile's model discovery path and returns model IDs.
// Also validates that the provider credentials work.
func providerValidationErrorMessage(providerID string, statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if providerID == "github_copilot" {
			return "Provider authentication failed. Token invalid, expired, missing Copilot entitlement, or org policy blocked access."
		}
		return "Provider authentication failed. Check API key or token and try again."
	case http.StatusNotFound:
		return "Model discovery failed. Check base URL; provider endpoint not found."
	default:
		return fmt.Sprintf("Model discovery failed: provider returned HTTP %d", statusCode)
	}
}

func (s *Server) fetchModelList(ctx context.Context, cfg *config.Config) ([]string, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	prof, err := provider.Get(cfg.Provider)
	if err != nil {
		return nil, err
	}
	if prof.APIKeyRequired && cfg.APIKey == "" {
		return nil, fmt.Errorf("%s is required for provider %q", prof.RequiredCredentialName(), cfg.Provider)
	}

	modelsURL := prof.ModelListURL(cfg.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	prof.ApplyHeaders(req, cfg.APIKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Provider unreachable. Check base URL and network connection: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(providerValidationErrorMessage(cfg.Provider, resp.StatusCode))
	}

	modelIDs, err := prof.ParseModelList(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Model discovery failed: %v", err)
	}
	if len(modelIDs) == 0 {
		return nil, fmt.Errorf("Model discovery failed: no selectable models returned")
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
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
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

	cfgState := s.loadConfigState(r.Context())
	if !cfgState.valid() {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		component := templates.ErrorToast(cfgState.err.Error())
		component.Render(r.Context(), w)
		return
	}
	if s.config.RunManager != nil {
		s.config.RunManager.UpdateProviderConfig(cfgState.cfg)
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
		Role:      "user",
		Content:   message,
		CreatedAt: time.Now(),
	})

	// Start run in background
	// Use context.Background() instead of r.Context() so the run survives
	// the HTTP handler returning (which cancels the request context).
	// CancelRun() provides explicit cancellation via state.Cancel().
	err := s.config.RunManager.StartRun(context.Background(), id, prompt)
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
	runState := s.config.RunManager.ActiveRun(id)
	if runState != nil {
		go func() {
			defer s.config.SessionManager.UpdateStatus(id, session.StatusIdle)
			w := NewSSEWriter(runState)
			s.config.RunManager.AppendEvent(runState, w)
		}()
	}

	// Render user bubble + send JS events for SSE connect and run state
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"eitri:connectRunStream":"`+id+`","eitri:runStarted":"`+id+`"}`)

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

	if s.config.RunManager == nil {
		http.Error(w, "No active run for this session", http.StatusNotFound)
		return
	}

	subscriberID, sseCh, ok := s.config.RunManager.subscribe(id)
	if !ok {
		http.Error(w, "No active run for this session", http.StatusNotFound)
		return
	}
	defer s.config.RunManager.unsubscribe(id, subscriberID)

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
			if evt.Type == "done" || evt.Type == "error" || evt.Type == "closed" {
				return
			}

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
	component := templates.MessageInput(id, false, false)
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

	messageID := ""
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var req struct {
			MessageID string `json:"message_id"`
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		messageID = req.MessageID
	} else {
		if err := r.ParseForm(); err != nil {
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		messageID = r.FormValue("message_id")
	}

	_ = messageID

	// Look up the last assistant message in session
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
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
			if isRequestTooLarge(err) {
				writeRequestTooLarge(w)
				return
			}
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		req.Name = r.FormValue("name")
		dataStr := r.FormValue("data")
		if dataStr != "" {
			if err := json.Unmarshal([]byte(dataStr), &req.Data); err != nil {
				// Bad JSON in data field — treat as empty
				req.Data = nil
			}
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
		slog.Warn("json marshal error", slog.Any("error", err))
		return nil
	}
	return b
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	registry := s.config.SkillsService.Registry()
	component := templates.SkillsPage(registry, s.config.Workspace, s.chatPathForRequest(r))
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
		"skills":      result,
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

	// Reject path-traversal and absolute paths for safety
	if strings.HasPrefix(q, "..") || strings.Contains(q, "/..") || strings.HasPrefix(q, "/") {
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

	models, err := s.fetchModelList(r.Context(), cfg)
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
