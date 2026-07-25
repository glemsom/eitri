package api

import (
	"net/http"
	"strconv"

	"github.com/glemsom/eitri/internal/report"
)

// handleListReports responds to GET /api/sessions/{id}/reports.
func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	// Build a report service from the persister
	if s.config.Persister == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sessionID,
			"runs":       []any{},
		})
		return
	}

	svc := report.New(s.config.Persister)
	runs, err := svc.ListRuns(sessionID)
	if err != nil {
		s.logger.Error("failed to list reports", "session_id", sessionID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []report.RunInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"runs":       runs,
	})
}

// handleGetReport responds to GET /api/sessions/{id}/report?run=N.
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	runIndex := 0
	if runStr := r.URL.Query().Get("run"); runStr != "" {
		var err error
		runIndex, err = strconv.Atoi(runStr)
		if err != nil || runIndex < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid run index"})
			return
		}
	}

	if s.config.Persister == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "persistence not configured"})
		return
	}

	svc := report.New(s.config.Persister)
	rep, err := svc.GetReport(sessionID, runIndex)
	if err != nil {
		s.logger.Error("failed to get report", "session_id", sessionID, "run", runIndex, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rep == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	writeJSON(w, http.StatusOK, rep)
}
