package api

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/glemsom/eitri/internal/api/templates"
)

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

	contextWindow := state.cfg.ContextWindowTokens
	if contextWindow == 0 {
		contextWindow = 256000 // default fallback
	}

	// Extract reasoning content from last assistant message for thinking panel
	var reasoningContent string
	if renderedSession != nil {
		for i := len(renderedSession.Messages) - 1; i >= 0; i-- {
			if renderedSession.Messages[i].Role == "assistant" {
				reasoningContent = renderedSession.Messages[i].ReasoningContent
				break
			}
		}
	}

	component := templates.ChatPage(sessions, id, renderedSession, sess.Workspace, configValid, r.URL.Path, contextWindow, reasoningContent)
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
	if s.config.RunService != nil {
		if err := s.config.RunService.CloseSession(id); err != nil {
			http.Error(w, "Failed to close session", http.StatusInternalServerError)
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
