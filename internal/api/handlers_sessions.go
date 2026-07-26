package api

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
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

	contextFiles := runner.ScanContextFiles(sess.Workspace)

	component := templates.ChatPage(sessions, id, renderedSession, sess.Workspace, configValid, r.URL.Path, contextWindow, reasoningContent, state.cfg.UserEmail, state.cfg.CompactionEnabled, state.cfg.ContextWarningThresholdPercent, contextFiles)
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

	// Save final snapshot before removing from memory
	if p := s.config.Persister; p != nil {
		if err := p.SnapshotSession(id, sess); err != nil {
			s.logger.Warn("failed to snapshot session on close",
				slog.String("session_id", id),
				slog.Any("error", err))
		}
	}

	s.config.SessionManager.Close(id)

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

// handlePermanentDelete permanently removes a session from both memory and disk.
// This is the "delete" action (vs close which keeps disk data).
// Wired from the gear menu Sessions page (ticket #5).
func (s *Server) handlePermanentDelete(w http.ResponseWriter, r *http.Request) {
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

	s.notifySessionClosed(id, "Session deleted")
	if s.config.RunService != nil {
		if err := s.config.RunService.CloseSession(id); err != nil {
			http.Error(w, "Failed to close session", http.StatusInternalServerError)
			return
		}
	}
	s.config.SessionManager.Delete(id)

	// Permanently remove persisted data from disk
	if p := s.config.Persister; p != nil {
		if err := p.DeleteSession(id); err != nil {
			s.logger.Warn("failed to delete persisted session data",
				slog.String("session_id", id),
				slog.Any("error", err))
		}
	}

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

// handleLoadSession loads a historical (closed) session back into the in-memory manager.
// POST /api/sessions/{id}/load
//
// If the session is already active, redirects to its chat view (no-op).
// If the session doesn't exist on disk, returns a 404 error toast.
// On success, swaps the sidebar and navigates to the loaded session's chat view.
func (s *Server) handleLoadSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	if browserID == "" {
		browserID = s.ensureBrowserID(w, r)
	}

	// Already active → redirect to chat (no-op)
	if sess := s.config.SessionManager.Get(id); sess != nil {
		s.hxRedirect(w, r, "/sessions/"+id)
		return
	}

	if s.config.RunService == nil {
		http.Error(w, "Run service not available", http.StatusInternalServerError)
		return
	}

	loaded, err := s.config.RunService.LoadSessionFromDisk(id)
	if err != nil {
		s.logger.Warn("failed to load session from disk",
			slog.String("session_id", id),
			slog.Any("error", err))
		w.WriteHeader(http.StatusNotFound)
		component := templates.ErrorToast("Session not found on disk")
		component.Render(r.Context(), w)
		return
	}

	// The loaded session may have a stale browser ID from when it was last active.
	// Update it to the current browser so it appears in the sidebar.
	if loaded.BrowserID != browserID {
		// Remove the old browser registration and re-add with current browser
		s.config.SessionManager.Delete(id)
		loaded.BrowserID = browserID
		// Reset ClosedAt so it appears as an active session
		loaded.ClosedAt = nil
		s.config.SessionManager.Add(loaded)
	}

	// Set HX-Redirect BEFORE writing any body content
	w.Header().Set("HX-Redirect", "/sessions/"+id)

	// Render the sidebar with the newly loaded session included
	sessions := s.config.SessionManager.ListByBrowser(browserID)
	sidebar := templates.SessionTabsList(sessions, id)
	sidebar.Render(r.Context(), w)
}

// handleSessionsPage renders the full Sessions management page at GET /sessions.
// It lists all active (in-memory) sessions plus all persisted sessions on disk.
func (s *Server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)

	// Gather active (in-memory) sessions for this browser
	var activeSessions []*session.UISession
	if browserID != "" {
		activeSessions = s.config.SessionManager.ListByBrowser(browserID)
	}

	// Gather disk sessions
	var diskRows []templates.SessionRow
	if s.config.Persister != nil {
		diskIDs, err := s.config.Persister.ListOnDiskSessionIDs()
		if err != nil {
			s.logger.Warn("failed to list disk sessions", slog.Any("error", err))
		} else {
			// Build a set of active IDs to exclude from disk listing
			activeIDs := make(map[string]bool, len(activeSessions))
			for _, as := range activeSessions {
				activeIDs[as.ID] = true
			}
			for _, id := range diskIDs {
				if activeIDs[id] {
					continue // already shown in active section
				}
				info, err := s.config.Persister.LoadSessionInfo(id)
				if err != nil {
					s.logger.Warn("failed to load session metadata",
						slog.String("session_id", id),
						slog.Any("error", err))
					continue
				}
				if info == nil {
					continue
				}
				duration := info.UpdatedAt.Sub(info.CreatedAt)
				if info.ClosedAt != nil {
					duration = info.ClosedAt.Sub(info.CreatedAt)
				}
				diskRows = append(diskRows, templates.SessionRow{
					ID:        info.ID,
					Title:     info.Title,
					TurnCount: info.Messages,
					CreatedAt: info.CreatedAt,
					UpdatedAt: info.UpdatedAt,
					Duration:  duration,
					IsActive:  false,
					IsClosed:  info.ClosedAt != nil,
				})
			}
		}
	}

	// Compute disk usage
	var diskUsageBytes int64
	if s.config.Persister != nil {
		du, err := s.config.Persister.DiskUsageBytes()
		if err != nil {
			s.logger.Warn("failed to compute disk usage", slog.Any("error", err))
		} else {
			diskUsageBytes = du
		}
	}

	component := templates.SessionsPage(activeSessions, diskRows, diskUsageBytes, s.chatPathForRequest(r), r.URL.Path, s.config.Workspace, 256000)
	component.Render(r.Context(), w)
}

// handleCleanupDeleteClosed permanently removes ALL closed sessions from disk and memory.
// POST /api/sessions/cleanup/delete-closed
func (s *Server) handleCleanupDeleteClosed(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)

	if s.config.Persister == nil {
		http.Error(w, "Persister not available", http.StatusInternalServerError)
		return
	}

	// Collect all session IDs to delete: closed in-memory sessions + closed disk sessions
	var toDelete []string

	// Closed in-memory sessions for this browser
	if browserID != "" {
		for _, sess := range s.config.SessionManager.ListByBrowser(browserID) {
			if sess.ClosedAt != nil {
				toDelete = append(toDelete, sess.ID)
			}
		}
	}

	// Closed disk sessions (not in memory)
	diskIDs, err := s.config.Persister.ListOnDiskSessionIDs()
	if err != nil {
		s.logger.Warn("failed to list disk sessions", slog.Any("error", err))
	} else {
		activeIDs := make(map[string]bool)
		if browserID != "" {
			for _, sess := range s.config.SessionManager.ListByBrowser(browserID) {
				activeIDs[sess.ID] = true
			}
		}
		for _, id := range diskIDs {
			if activeIDs[id] {
				continue
			}
			info, err := s.config.Persister.LoadSessionInfo(id)
			if err != nil || info == nil {
				continue
			}
			if info.ClosedAt != nil {
				toDelete = append(toDelete, id)
			}
		}
	}

	// Perform deletion
	var deleted int
	for _, id := range toDelete {
		// Close in-memory (if active)
		if s.config.RunService != nil {
			_ = s.config.RunService.CloseSession(id)
		}
		s.config.SessionManager.Delete(id)
		if err := s.config.Persister.DeleteSession(id); err != nil {
			s.logger.Warn("failed to delete session data",
				slog.String("session_id", id),
				slog.Any("error", err))
			continue
		}
		deleted++
	}

	// Build updated sessions list for HTMX response
	var activeSessions []*session.UISession
	if browserID != "" {
		activeSessions = s.config.SessionManager.ListByBrowser(browserID)
	}

	remainingDiskIDs, _ := s.config.Persister.ListOnDiskSessionIDs()
	activeIDSet := make(map[string]bool, len(activeSessions))
	for _, as := range activeSessions {
		activeIDSet[as.ID] = true
	}
	var diskRows []templates.SessionRow
	for _, id := range remainingDiskIDs {
		if activeIDSet[id] {
			continue
		}
		info, err := s.config.Persister.LoadSessionInfo(id)
		if err != nil || info == nil {
			continue
		}
		duration := info.UpdatedAt.Sub(info.CreatedAt)
		if info.ClosedAt != nil {
			duration = info.ClosedAt.Sub(info.CreatedAt)
		}
		diskRows = append(diskRows, templates.SessionRow{
			ID:        info.ID,
			Title:     info.Title,
			TurnCount: info.Messages,
			CreatedAt: info.CreatedAt,
			UpdatedAt: info.UpdatedAt,
			Duration:  duration,
			IsClosed:  info.ClosedAt != nil,
		})
	}

	component := templates.SessionsList(activeSessions, diskRows)
	component.Render(r.Context(), w)
}

// handleCleanupClearAllTraces removes all trace files from all sessions on disk.
// POST /api/sessions/cleanup/clear-all-traces
func (s *Server) handleCleanupClearAllTraces(w http.ResponseWriter, r *http.Request) {
	if s.config.Persister == nil {
		http.Error(w, "Persister not available", http.StatusInternalServerError)
		return
	}

	diskIDs, err := s.config.Persister.ListOnDiskSessionIDs()
	if err != nil {
		http.Error(w, "Failed to list sessions", http.StatusInternalServerError)
		return
	}

	var cleared int
	for _, id := range diskIDs {
		traces, err := s.config.Persister.ListTraces(id)
		if err != nil {
			continue
		}
		for _, traceID := range traces {
			tracePath := filepath.Join(s.config.Persister.RootDir(), "sessions", id, "traces", traceID+".json")
			if err := os.Remove(tracePath); err == nil {
				cleared++
			}
		}
	}

	// Re-render cleanup section with refreshed disk usage
	diskUsageBytes, _ := s.config.Persister.DiskUsageBytes()
	component := templates.SessionsCleanup(diskUsageBytes)
	component.Render(r.Context(), w)
}

// handleCleanupPruneByAge permanently removes closed sessions older than the specified age.
// POST /api/sessions/cleanup/prune-by-age
func (s *Server) handleCleanupPruneByAge(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)

	if s.config.Persister == nil {
		http.Error(w, "Persister not available", http.StatusInternalServerError)
		return
	}

	// Parse age-in-days from form value
	ageDaysStr := r.FormValue("age_days")
	if ageDaysStr == "" {
		http.Error(w, "age_days parameter is required", http.StatusBadRequest)
		return
	}
	ageDays, err := strconv.Atoi(ageDaysStr)
	if err != nil || ageDays < 1 {
		http.Error(w, "age_days must be a positive integer", http.StatusBadRequest)
		return
	}
	cutoff := time.Now().Add(-time.Duration(ageDays) * 24 * time.Hour)

	// Collect closed session IDs past the age threshold
	var toDelete []string

	// Closed in-memory sessions for this browser
	if browserID != "" {
		for _, sess := range s.config.SessionManager.ListByBrowser(browserID) {
			if sess.ClosedAt != nil && sess.ClosedAt.Before(cutoff) {
				toDelete = append(toDelete, sess.ID)
			}
		}
	}

	// Closed disk sessions (not in memory)
	diskIDs, err := s.config.Persister.ListOnDiskSessionIDs()
	if err != nil {
		s.logger.Warn("failed to list disk sessions", slog.Any("error", err))
	} else {
		activeIDs := make(map[string]bool)
		if browserID != "" {
			for _, sess := range s.config.SessionManager.ListByBrowser(browserID) {
				activeIDs[sess.ID] = true
			}
		}
		for _, id := range diskIDs {
			if activeIDs[id] {
				continue
			}
			info, err := s.config.Persister.LoadSessionInfo(id)
			if err != nil || info == nil {
				continue
			}
			if info.ClosedAt != nil && info.ClosedAt.Before(cutoff) {
				toDelete = append(toDelete, id)
			}
		}
	}

	// Perform deletion
	var deleted int
	for _, id := range toDelete {
		if s.config.RunService != nil {
			_ = s.config.RunService.CloseSession(id)
		}
		s.config.SessionManager.Delete(id)
		if err := s.config.Persister.DeleteSession(id); err != nil {
			s.logger.Warn("failed to delete session data",
				slog.String("session_id", id),
				slog.Any("error", err))
			continue
		}
		deleted++
	}

	// Build updated sessions list for HTMX response
	var activeSessions []*session.UISession
	if browserID != "" {
		activeSessions = s.config.SessionManager.ListByBrowser(browserID)
	}

	remainingDiskIDs, _ := s.config.Persister.ListOnDiskSessionIDs()
	activeIDSet := make(map[string]bool, len(activeSessions))
	for _, as := range activeSessions {
		activeIDSet[as.ID] = true
	}
	var diskRows []templates.SessionRow
	for _, id := range remainingDiskIDs {
		if activeIDSet[id] {
			continue
		}
		info, err := s.config.Persister.LoadSessionInfo(id)
		if err != nil || info == nil {
			continue
		}
		duration := info.UpdatedAt.Sub(info.CreatedAt)
		if info.ClosedAt != nil {
			duration = info.ClosedAt.Sub(info.CreatedAt)
		}
		diskRows = append(diskRows, templates.SessionRow{
			ID:        info.ID,
			Title:     info.Title,
			TurnCount: info.Messages,
			CreatedAt: info.CreatedAt,
			UpdatedAt: info.UpdatedAt,
			Duration:  duration,
			IsClosed:  info.ClosedAt != nil,
		})
	}

	// Render both the sessions list (for the list swap) and cleanup (for stats)
	// We swap the sessions list outerHTML and the cleanup section via OOB swap
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = templates.SessionsList(activeSessions, diskRows).Render(r.Context(), w)
	diskUsageBytes, _ := s.config.Persister.DiskUsageBytes()

	// Render cleanup section as OOB swap to update disk usage
	// Use a wrapping div with hx-swap-oob for HTMX out-of-band swap
	_, _ = fmt.Fprintf(w, `<div id="sessions-cleanup" hx-swap-oob="true">`)
	_ = templates.SessionsCleanup(diskUsageBytes).Render(r.Context(), w)
	_, _ = fmt.Fprintf(w, `</div>`)
}
