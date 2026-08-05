package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	ConfigPath string // path to config file for save
	Workspace  string // launch workspace (process CWD)
	// HomeDir is the user home directory used for persona storage
	// (~/.eitri/personas/). If empty, it falls back to os.UserHomeDir().
	// Tests inject a per-server home dir instead of mutating the process HOME
	// env var (issue #1023).
	HomeDir        string
	SessionManager *session.Manager
	RunService     *runner.RunService
	SkillsService  *skills.Service
	Logger         *slog.Logger
	CopilotOAuth   GitHubCopilotOAuthConfig
	Version        string             // injected at build time
	StartTime      time.Time          // server start timestamp
	DebugRecorder  *debug.Recorder    // optional HTTP trace recorder
	Persister      *persist.Persister // optional; deletes session data from disk on session delete
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config        ServerConfig
	mux           *http.ServeMux
	httpClient    *http.Client
	logger        *slog.Logger
	copilotOAuth  GitHubCopilotOAuthConfig
	copilotFlows  *copilotDeviceFlowStore
	persistAuthFn provider.PersistAuthFunc
}

const maxRequestBodyBytes = 1 << 20

// staticAssetCacheControl is applied to every successful /static/* response.
// The embedded assets are content-addressed via the ?v= cache-bust query
// string on all references (see templates/staticAsset), so the bytes behind
// any given URL never change — a one-year immutable cache is safe and avoids
// re-downloading the ~4.7MB bundle on every navigation/reload. (issue #969)
const staticAssetCacheControl = "public, max-age=31536000, immutable"

// cacheControlWriter attaches Cache-Control to successful responses written by
// the wrapped handler. http.FileServer writes headers itself, so the header is
// set on first WriteHeader/Write rather than before delegating.
type cacheControlWriter struct {
	http.ResponseWriter
	headerWritten bool
}

func (w *cacheControlWriter) WriteHeader(status int) {
	if !w.headerWritten {
		if status >= 200 && status < 300 {
			w.Header().Set("Cache-Control", staticAssetCacheControl)
		}
		w.headerWritten = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *cacheControlWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

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

	// Resolve the persona home directory once at construction so all handlers
	// share the server's home dir instead of re-reading the process env. An
	// empty HomeDir keeps production behavior: fall back to os.UserHomeDir().
	if cfg.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.HomeDir = home
		}
	}
	if cfg.RunService != nil && cfg.HomeDir != "" {
		cfg.RunService.SetHomeDir(cfg.HomeDir)
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

// assetVersionedContent replaces the cache-bust placeholder embedded in PWA
// files (sw.js, manifest.json) with the current asset version so that their
// internal static references and the service-worker cache name stay in lockstep
// with the versioned URLs rendered by the page shell.
func (s *Server) assetVersionedContent(content string) string {
	return strings.ReplaceAll(content, "__EITRI_VERSION__", assets.CacheBustVersion)
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
	// Static assets are served with a long-lived immutable Cache-Control. All
	// references carry a ?v=<content-hash> cache-bust query string, so stale
	// copies are impossible after a release. (issue #969)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.FileServerFS(assets.Files).ServeHTTP(&cacheControlWriter{ResponseWriter: w}, r)
	})))

	// PWA: manifest.json served directly from embedded assets. Icon URLs inside
	// it are versioned via the cache-bust placeholder so they can be served
	// immutable without going stale.
	s.mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		data, err := assets.Files.ReadFile("manifest.json")
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(s.assetVersionedContent(string(data))))
	})

	// PWA: sw.js with required Service-Worker-Allowed header. The cache name
	// and precache URLs embed the asset cache-bust version, so a release both
	// updates the service worker (fresh script, no-cache) and invalidates its
	// precache. (issue #969)
	s.mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		data, err := assets.Files.ReadFile("sw.js")
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(s.assetVersionedContent(string(data))))
	})

	// Root serves the base HTML page — redirects to session if browser known
	s.mux.HandleFunc("GET /{$}", s.handleRoot)

	// Sessions
	s.mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.handleCloseSession)
	s.mux.HandleFunc("POST /api/sessions/{id}/load", s.handleLoadSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}/permanent", s.handlePermanentDelete)

	// Sessions page (issue #800)
	s.mux.HandleFunc("GET /sessions", s.handleSessionsPage)
	s.mux.HandleFunc("POST /api/sessions/cleanup/delete-closed", s.handleCleanupDeleteClosed)
	s.mux.HandleFunc("POST /api/sessions/cleanup/clear-all-traces", s.handleCleanupClearAllTraces)
	s.mux.HandleFunc("POST /api/sessions/cleanup/prune-by-age", s.handleCleanupPruneByAge)

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
	s.mux.HandleFunc("GET /api/sessions/{id}/skills/chips", s.handleSessionSkillChips)
	s.mux.HandleFunc("POST /api/skills/{name}/disable", s.handleDisableSkill)
	s.mux.HandleFunc("POST /api/skills/{name}/enable", s.handleEnableSkill)
	s.mux.HandleFunc("POST /api/skills/disable-all", s.handleDisableAllSkills)
	s.mux.HandleFunc("POST /api/skills/enable-all", s.handleEnableAllSkills)

	// Directory browser endpoint (issue #627)
	s.mux.HandleFunc("GET /api/browse-directory", s.handleBrowseDirectory)

	// Directory browser overlay + workspace update (issue #628)
	s.mux.HandleFunc("GET /api/sessions/{id}/directory-browser", s.handleSessionDirectoryBrowser)
	s.mux.HandleFunc("POST /api/sessions/{id}/workspace", s.handleUpdateWorkspace)

	// Persona CRUD (issue #754)
	s.mux.HandleFunc("GET /api/personas", s.handleGetPersonas)
	s.mux.HandleFunc("GET /api/personas/add-form", s.handleGetPersonaAddForm)
	s.mux.HandleFunc("POST /api/personas", s.handleCreatePersona)
	s.mux.HandleFunc("GET /api/personas/{name}", s.handleGetPersona)
	s.mux.HandleFunc("PUT /api/personas/{name}", s.handleUpdatePersona)
	s.mux.HandleFunc("DELETE /api/personas/{name}", s.handleDeletePersona)

	// Persona selector fragment for header (issue #755)
	s.mux.HandleFunc("GET /api/personas/selector", s.handlePersonaSelector)
	s.mux.HandleFunc("POST /api/personas/activate", s.handleActivatePersona)

	// Manual compaction trigger (issue #723)
	s.mux.HandleFunc("POST /api/sessions/{id}/compact", s.handleCompact)

	// Session Report endpoints (issue #791)
	s.mux.HandleFunc("GET /api/sessions/{id}/reports", s.handleListReports)
	s.mux.HandleFunc("GET /api/sessions/{id}/report", s.handleGetReport)

	// Session Report UI page (issue #792)
	s.mux.HandleFunc("GET /report/{id}", s.handleReportPage)
	s.mux.HandleFunc("GET /api/report/{id}/fragment", s.handleReportFragment)

	// Browser-level event stream for real-time UI updates (issue #514)
	s.mux.HandleFunc("GET /api/events", s.handleBrowserEvents)

	// Session tabs fragment for OOB swap updates
	s.mux.HandleFunc("GET /api/session-tabs", s.handleSessionTabs)

	// Debug API routes (issue #556)
	s.mux.HandleFunc("GET /api/debug/sessions", s.handleDebugSessions)
	s.mux.HandleFunc("GET /api/debug/sessions/{id}", s.handleDebugSessionByID)
	s.mux.HandleFunc("GET /api/debug/sessions/{id}/http", s.handleDebugSessionHTTP)
	s.mux.HandleFunc("GET /api/debug/runtime", s.handleDebugRuntime)
	s.mux.HandleFunc("GET /api/debug/config", s.handleDebugConfig)
	s.mux.HandleFunc("GET /api/debug/health", s.handleDebugHealth)
	s.mux.HandleFunc("GET /api/debug", s.handleDebugUmbrella)
	s.mux.HandleFunc("GET /api/debug/http", s.handleDebugHTTP)
	s.mux.HandleFunc("GET /api/debug/http/{trace_id}", s.handleDebugHTTPByID)

	// Session file serving for screenshot images and other workspace files (issue #924)
	s.mux.HandleFunc("GET /sessions/{id}/files/{filename}", s.handleSessionFile)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

var sessionScreenshotFilenamePattern = regexp.MustCompile(`^browser-screenshot-[0-9]+\.png$`)

// handleSessionFile serves screenshot files from a session's workspace directory.
// Security: validates browser ownership and only serves files matching the
// browser-screenshot-*.png pattern.
func (s *Server) handleSessionFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	filename := r.PathValue("filename")

	browserID := s.browserIDFromRequest(r)
	if meta := s.config.SessionManager.GetMetaShared(id); meta == nil || meta.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if !sessionScreenshotFilenamePattern.MatchString(filename) {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	cfg := s.config.SessionManager.GetConfigShared(id)
	if cfg == nil || cfg.Workspace == "" {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	filePath := filepath.Join(cfg.Workspace, filename)

	// Security: ensure the resolved path is within the workspace directory.
	absWorkspace, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		http.Error(w, "Invalid workspace", http.StatusInternalServerError)
		return
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	rel, err := filepath.Rel(absWorkspace, absFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, filePath)
}
