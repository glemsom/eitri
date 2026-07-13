package api

import (
	"encoding/json"
	"net/http"

	"github.com/glemsom/eitri/internal/api/assets"
	"github.com/glemsom/eitri/internal/api/templates"
)

// ServerConfig holds dependencies and settings for the API server.
type ServerConfig struct {
	// Add fields as features are implemented
}

// Server wraps the HTTP handler and injected dependencies.
type Server struct {
	config ServerConfig
	mux    *http.ServeMux
}

// NewServer creates a new Server with routes registered.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		config: cfg,
		mux:    http.NewServeMux(),
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
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	component := templates.Base("Eitri — AI Assistant")
	component.Render(r.Context(), w)
}
