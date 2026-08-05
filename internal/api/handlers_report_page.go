package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/report"
	"github.com/glemsom/eitri/internal/session"
)

// handleReportPage renders the full Session Report page at /report/{id}.
func (s *Server) handleReportPage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	if browserID == "" {
		browserID = s.ensureBrowserID(w, r)
	}

	// Get session metadata from in-memory session manager via the shared read
	// accessors. The report body renders from the persisted report (rep); the
	// in-memory session contributes only the workspace and the "session not
	// found" fallback, so a meta-only facade is enough — no conversation copy.
	meta := s.config.SessionManager.GetMetaShared(sessionID)
	cfg := s.config.SessionManager.GetConfigShared(sessionID)
	state := s.loadConfigState(r.Context())

	sessions := s.config.SessionManager.ListByBrowser(browserID)

	var (
		rep       *report.SessionReport
		runs      []report.RunInfo
		turnViews []templates.TurnView
	)

	// Determine selected run index from query param
	selectedRun := 0
	if runStr := r.URL.Query().Get("run"); runStr != "" {
		if v, err := strconv.Atoi(runStr); err == nil && v >= 0 {
			selectedRun = v
		}
	}

	if s.config.Persister != nil {
		svc := report.New(s.config.Persister)

		// List available runs
		var listErr error
		runs, listErr = svc.ListRuns(sessionID)
		if listErr != nil {
			s.logger.Warn("failed to list runs for report page",
				slog.String("session_id", sessionID),
				slog.Any("error", listErr))
		}
		if runs == nil {
			runs = []report.RunInfo{}
		}

		// Get full report for the selected run
		var getErr error
		rep, getErr = svc.GetReport(sessionID, selectedRun)
		if getErr != nil {
			s.logger.Warn("failed to get report for page",
				slog.String("session_id", sessionID),
				slog.Int("run", selectedRun),
				slog.Any("error", getErr))
		}

		// Pre-render markdown content for each turn
		if rep != nil {
			turnViews = makeTurnViews(rep)
		}
	}

	// Determine workspace for sidebar display
	workspace := ""
	if cfg != nil {
		workspace = cfg.Workspace
	}

	contextWindow := 256000
	if state.cfg != nil && state.cfg.ContextWindowTokens > 0 {
		contextWindow = state.cfg.ContextWindowTokens
	}

	// The ReportPage template only nil-checks the session facade to decide
	// whether to show the "session data not found" fallback, so a meta-only
	// facade is sufficient here (shared accessors, no conversation copy).
	var sess *session.UISession
	if meta != nil {
		sess = &session.UISession{
			ID:        meta.ID,
			BrowserID: meta.BrowserID,
			ParentID:  meta.ParentID,
			Title:     meta.Title,
			Status:    meta.Status,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			ClosedAt:  meta.ClosedAt,
			Workspace: workspace,
		}
	}

	activeID := sessionID
	if meta == nil && len(sessions) > 0 {
		activeID = sessions[0].ID
	}

	component := templates.ReportPage(sessions, activeID, sess, workspace, r.URL.Path, contextWindow, "", false, 0, rep, runs, selectedRun, turnViews)
	component.Render(r.Context(), w)
}

// handleReportFragment renders the timeline fragment for HTMX run selector swaps.
func (s *Server) handleReportFragment(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	runStr := r.URL.Query().Get("run")

	selectedRun := 0
	if runStr != "" {
		if v, err := strconv.Atoi(runStr); err == nil && v >= 0 {
			selectedRun = v
		}
	}

	if s.config.Persister == nil {
		http.Error(w, "Persistence not configured", http.StatusNotFound)
		return
	}

	svc := report.New(s.config.Persister)
	rep, err := svc.GetReport(sessionID, selectedRun)
	if err != nil || rep == nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_ = templates.ReportNotFound().Render(r.Context(), w)
		return
	}

	turnViews := makeTurnViews(rep)

	w.Header().Set("Content-Type", "text/html")
	_ = templates.ReportTimeline(turnViews).Render(r.Context(), w)
}

// makeTurnViews pre-renders markdown content for each turn in the report.
func makeTurnViews(rep *report.SessionReport) []templates.TurnView {
	views := make([]templates.TurnView, 0, len(rep.Turns))
	for _, turn := range rep.Turns {
		view := templates.TurnView{
			Turn:             turn.Turn,
			Role:             turn.Role,
			ContentHTML:      renderMarkdownToHTML(turn.Content),
			ReasoningHTML:    renderMarkdownToHTML(turn.ReasoningContent),
			Timestamp:        turn.Timestamp,
			LLMDurationMs:    turn.LLMDurationMs,
			LLMTraceID:       turn.LLMTraceID,
			LLMRequestBytes:  turn.LLMRequestBytes,
			LLMResponseBytes: turn.LLMResponseBytes,
			ContextBefore:    turn.ContextBefore,
			ContextAfter:     turn.ContextAfter,
			ToolCalls:        turn.ToolCalls,
		}
		views = append(views, view)
	}
	return views
}
