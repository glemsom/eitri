package api

import (
	"fmt"
	"net/http"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/runner/runconfig"
)

// handleCompact manually triggers compaction for a session's conversation history.
// It is invoked by the "Compact now" button in the sidebar context panel or by the
// /compact slash command. It does NOT start an agent run — the turn counter is not
// incremented.
//
// The handler:
//  1. Loads the current config
//  2. Builds a minimal LLM service for summarization
//  3. Reads the session's conversation history from the history session manager
//  4. Runs the compactor (same engine used by auto-compaction)
//  5. Replaces the in-memory history with the compacted version
//  6. Snapshots the compacted history to disk (if persister is available)
//  7. Returns a toast message with compaction stats
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if s.config.RunService == nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.ErrorToast("Run service not available").Render(r.Context(), w)
		return
	}

	// Load config directly without model discovery — compaction doesn't need it.
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_ = templates.ErrorToast("Failed to load config: " + err.Error()).Render(r.Context(), w)
		return
	}
	if err := config.Validate(cfg); err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = templates.ErrorToast("Config invalid: " + err.Error()).Render(r.Context(), w)
		return
	}

	// Allow manual compaction even when auto-compaction is disabled.
	// The user can still manually compact via the "Compact now" button.

	runCfg := runconfig.FromConfig(cfg, sess.Workspace, 0)

	count, freed, err := s.config.RunService.CompactSession(r.Context(), id, runCfg)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_ = templates.ErrorToast("Compaction failed: " + err.Error()).Render(r.Context(), w)
		return
	}

	if count == 0 {
		// No tool results found to compact
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_ = templates.ErrorToast("No tool results found to compact").Render(r.Context(), w)
		return
	}

	freedK := freed / 1000
	toastMsg := fmt.Sprintf("Compacted %d messages — freed ~%dk tokens", count, freedK)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Retarget", "#error-toasts")
	w.WriteHeader(http.StatusOK)
	_ = templates.ErrorToast(toastMsg).Render(r.Context(), w)
}
