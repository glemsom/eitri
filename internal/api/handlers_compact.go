package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/runner"
)

// compactTimeout is the maximum time allowed for a single manual compaction
// request. Compaction runs N sequential LLM calls (one per candidate message
// before the batching change), so we give it a generous budget to complete.
// If the timeout is exceeded, a user-visible error toast is returned and
// no history is modified.
const compactTimeout = 120 * time.Second

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
	// Apply a timeout to prevent the handler from hanging indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), compactTimeout)
	defer cancel()

	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	meta := s.config.SessionManager.GetMetaShared(id)
	if meta == nil || meta.BrowserID != browserID {
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

	cfgState := s.config.SessionManager.GetConfigShared(id)
	workspace := ""
	if cfgState != nil {
		workspace = cfgState.Workspace
	}
	runCfg := runner.FromConfig(cfg, workspace, 0)
	runCfg.HomeDir = s.config.HomeDir // persona storage home (issue #1023)

	count, freed, prunedToolCalls, err := s.config.RunService.CompactSession(ctx, id, runCfg)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_ = templates.ErrorToast("Compaction failed: " + err.Error()).Render(r.Context(), w)
		return
	}

	if count == 0 && prunedToolCalls == 0 {
		// No results found to compact or prune
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_ = templates.ErrorToast("No messages found to compact").Render(r.Context(), w)
		return
	}

	freedK := freed / 1000
	toastMsg := fmt.Sprintf("Compacted %d messages — freed ~%dk tokens", count, freedK)
	if prunedToolCalls > 0 {
		toastMsg += fmt.Sprintf(". %d tool calls pruned", prunedToolCalls)
	}

	// Re-render the messages list so the UI reflects the compaction immediately.
	// We render two elements in the response:
	//   1. The toast (main target, goes to #error-toasts)
	//   2. The messages container via OOB swap (replaces #messages in-place)
	// Uses the explicit copy helper: renderSessionForPage needs the assembled
	// UISession facade (meta + messages + skills in one struct) to produce the
	// HTML, and the render may run concurrently with an active agent run.
	sess := s.config.SessionManager.CopySession(id)
	if sess != nil {
		renderedSess := renderSessionForPage(sess)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		// Toast as the primary target (HX-Retarget ensures it lands in #error-toasts).
		_ = templates.ErrorToast(toastMsg).Render(r.Context(), w)
		// Messages container via OOB swap — HTMX processes this regardless of the
		// hx-target because the element has id="messages" and the fragment carries
		// hx-swap-oob="true" (rendered by the template).
		_ = templates.ChatMessages(renderedSess, cfg.UserEmail).Render(r.Context(), w)
		return
	}

	// Fallback: session not found (shouldn't happen), just show the toast.
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_ = templates.ErrorToast(toastMsg).Render(r.Context(), w)
}
