package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath     string // path to config file for save
	Workspace      string // launch workspace (process CWD)
	SessionManager *session.Manager
	RunService     *runner.RunService
	SkillsService  *skills.Service
	Logger         *slog.Logger
	CopilotOAuth   GitHubCopilotOAuthConfig
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config          ServerConfig
	mux             *http.ServeMux
	httpClient      *http.Client
	logger          *slog.Logger
	copilotOAuth    GitHubCopilotOAuthConfig
	copilotFlows    *copilotDeviceFlowStore
	persistAuthFn   provider.PersistAuthFunc
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

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (s *Server) notifySessionClosed(sessionID, message string) {
	if s.config.RunService == nil {
		return
	}
	s.config.RunService.NotifySessionClosed(sessionID, message)
}

// CloseActiveStreams notifies all attached SSE clients that their stream is closing.
func (s *Server) CloseActiveStreams(message string) {
	if s.config.RunService == nil {
		return
	}
	s.config.RunService.NotifyAllStreamsClosed(message)
}

// NewServer creates a new Server with routes registered.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		config:       cfg,
		mux:          http.NewServeMux(),
		logger:       logger,
		copilotOAuth: provider.DefaultGitHubCopilotOAuthConfig(cfg.CopilotOAuth),
		copilotFlows: newCopilotDeviceFlowStore(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	if cfg.RunService != nil {
		s.persistAuthFn = func(apiKey string, providerAuth json.RawMessage) error {
			cfg2, err := config.Load(cfg.ConfigPath)
			if err != nil {
				return fmt.Errorf("failed to load config for auth persist: %w", err)
			}
			cfg2.APIKey = apiKey
			cfg2.ProviderAuth = append(json.RawMessage(nil), providerAuth...)
			if err := config.Save(cfg.ConfigPath, cfg2); err != nil {
				return fmt.Errorf("failed to save refreshed provider auth: %w", err)
			}
			cfg.RunService.InvalidateRunners()
			return nil
		}
		cfg.RunService.SetPersistAuth(s.persistAuthFn)
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
	s.mux.HandleFunc("POST /api/providers/github_copilot/device-flow/start", s.handleStartCopilotDeviceFlow)
	s.mux.HandleFunc("GET /api/providers/github_copilot/device-flow/{id}", s.handlePollCopilotDeviceFlow)
	s.mux.HandleFunc("DELETE /api/providers/github_copilot/device-flow/{id}", s.handleCancelCopilotDeviceFlow)

	// Agent run + SSE streaming (issue #6)
	s.mux.HandleFunc("POST /api/sessions/{id}/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/sessions/{id}/stream", s.handleStream)
	s.mux.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("POST /api/sessions/{id}/render", s.handleRender)

	// Confirmation endpoint (issue #313)
	s.mux.HandleFunc("POST /api/sessions/{id}/confirm", s.handleConfirm)

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
