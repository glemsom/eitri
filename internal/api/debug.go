package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/runstate"
)

// debugSessionSummary is the shape returned by GET /api/debug/sessions.
type debugSessionSummary struct {
	ID                   string              `json:"id"`
	Title                string              `json:"title"`
	Status               string              `json:"status"`
	MessageCount         int                 `json:"message_count"`
	ActiveSkills         []string            `json:"active_skills"`
	Run                  *runInfo            `json:"run,omitempty"`
	LatestHTTP           []*debug.HTTPTrace  `json:"latest_http"`
	LastMessageTimestamp time.Time           `json:"last_message_timestamp"`
}

type runInfo struct {
	Status       string `json:"status"`
	SSESubscriberCount uint64 `json:"sse_subscriber_count"`
	SSEReplayCount     uint64 `json:"sse_replay_count"`
}

// debugSessionDetail is the shape returned by GET /api/debug/sessions/{id}.
type debugSessionDetail struct {
	Session      debugSessionSummary `json:"session"`
	Messages     []session.Message   `json:"messages"`
	ActiveSkills []string            `json:"active_skills"`
	Run          *runInfo            `json:"run,omitempty"`
	SSEHistory []runstate.SSEEvent `json:"sse_history,omitempty"`
}

// debugRuntimeResponse is the shape returned by GET /api/debug/runtime.
type debugRuntimeResponse struct {
	Version            string                  `json:"version"`
	UpSince            time.Time               `json:"up_since"`
	ActiveRunCount     int                     `json:"active_run_count"`
	SessionCount       int                     `json:"session_count"`
	RecordedHTTPTraces int                     `json:"recorded_http_traces"`
	ConfigSummary      *sanitizedConfig        `json:"config_summary"`
	ActiveSessions     []activeSessionSSEInfo  `json:"active_sessions,omitempty"`
}

type activeSessionSSEInfo struct {
	SessionID          string `json:"session_id"`
	SSESubscriberCount uint64 `json:"sse_subscriber_count"`
	SSEReplayCount     uint64 `json:"sse_replay_count"`
}

// sanitizedConfig exposes safe config fields (no secrets).
type sanitizedConfig struct {
	ProviderID          string `json:"provider_id"`
	Model               string `json:"model"`
	BaseURL             string `json:"base_url"`
	ContextWindowTokens int    `json:"context_window_tokens"`
	MaxTurns            int    `json:"max_turns"`
	CommandTimeout      int64  `json:"command_timeout"`
	HasAPIKey           bool   `json:"has_api_key"`
}

// writeJSON is a helper to write a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleDebugSessions(w http.ResponseWriter, r *http.Request) {
	allSessions := s.config.SessionManager.All()
	summaries := make([]debugSessionSummary, 0, len(allSessions))
	for _, sess := range allSessions {
		summary := sessionToSummary(sess)
		// Enrich with SSE counters if run active
		if summary.Run != nil && s.config.RunService != nil {
			if active := s.config.RunService.ActiveRun(sess.ID); active != nil {
				summary.Run.SSESubscriberCount = active.SSE.SubscriberCount()
				summary.Run.SSEReplayCount = active.SSE.ReplayCount()
			}
		}
		// Enrich with latest HTTP traces
		if s.config.DebugRecorder != nil {
			summary.LatestHTTP = s.config.DebugRecorder.LastN(sess.ID, 3)
		}
		summaries = append(summaries, summary)
	}
	if summaries == nil {
		summaries = []debugSessionSummary{}
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (s *Server) handleDebugSessionByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	sess := s.config.SessionManager.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Support ?limit_messages=N
	msgCount := len(sess.Messages)
	if limitStr := r.URL.Query().Get("limit_messages"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n >= 0 && n < msgCount {
			msgCount = n
		}
	}

	messages := sess.Messages
	if msgCount < len(messages) {
		messages = messages[len(messages)-msgCount:]
	}
	if messages == nil {
		messages = []session.Message{}
	}

	detail := debugSessionDetail{
		Session:      sessionToSummary(sess),
		Messages:     messages,
		ActiveSkills: sess.ActiveSkills,
	}
	// Enrich with latest HTTP traces
	if s.config.DebugRecorder != nil {
		detail.Session.LatestHTTP = s.config.DebugRecorder.LastN(id, 3)
	}
	if sess.Status != session.StatusIdle {
		detail.Run = &runInfo{Status: string(sess.Status)}
		if s.config.RunService != nil {
			if active := s.config.RunService.ActiveRun(id); active != nil {
				detail.Run.SSESubscriberCount = active.SSE.SubscriberCount()
				detail.Run.SSEReplayCount = active.SSE.ReplayCount()
				// SSE history truncated to last 50 events
				history := active.SSE.History()
				if len(history) > 50 {
					history = history[len(history)-50:]
				}
				detail.SSEHistory = history
			}
	}
	}

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleDebugRuntime(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadConfig()
	cfgSummary := sanitizeConfig(cfg)

	activeRunCount := 0
	var activeSessions []activeSessionSSEInfo
	if s.config.RunService != nil {
		activeRunCount = s.config.RunService.ActiveRunCount()
		counters := s.config.RunService.ActiveRunSSECounters()
		if len(counters) > 0 {
			activeSessions = make([]activeSessionSSEInfo, 0, len(counters))
			for sessionID, c := range counters {
				activeSessions = append(activeSessions, activeSessionSSEInfo{
					SessionID:          sessionID,
					SSESubscriberCount: c.SubscriberCount,
					SSEReplayCount:     c.ReplayCount,
				})
			}
		}
	}

	recordedTraces := 0
	if s.config.DebugRecorder != nil {
		recordedTraces = s.config.DebugRecorder.Count()
	}

	resp := debugRuntimeResponse{
		Version:            s.config.Version,
		UpSince:            s.config.StartTime,
		ActiveRunCount:     activeRunCount,
		SessionCount:       s.config.SessionManager.Count(),
		RecordedHTTPTraces: recordedTraces,
		ConfigSummary:      cfgSummary,
		ActiveSessions:     activeSessions,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadConfig()
	cfgSummary := sanitizeConfig(cfg)

	resp := struct {
		*sanitizedConfig
		CompletedRunRetentionMs int64 `json:"completed_run_retention_ms,omitempty"`
	}{
		sanitizedConfig:         cfgSummary,
		CompletedRunRetentionMs: 0,
	}

	if s.config.RunService != nil {
		resp.CompletedRunRetentionMs = s.config.RunService.CompletedRunRetentionMs()
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDebugHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// loadConfig reads config from disk. Returns defaults on error.
func (s *Server) loadConfig() *config.Config {
	if s.config.ConfigPath == "" {
		cfg := config.Defaults()
		return &cfg
	}
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		cfg := config.Defaults()
		return &cfg
	}
	return cfg
}

// sanitizeConfig returns a sanitized config (no secrets exposed).
func sanitizeConfig(cfg *config.Config) *sanitizedConfig {
	if cfg == nil {
		return nil
	}
	return &sanitizedConfig{
		ProviderID:          cfg.Provider,
		Model:               cfg.Model,
		BaseURL:             cfg.BaseURL,
		ContextWindowTokens: cfg.ContextWindowTokens,
		MaxTurns:            cfg.MaxTurns,
		CommandTimeout:      cfg.CommandTimeout,
		HasAPIKey:           cfg.APIKey != "" || len(cfg.ProviderAuth) > 0,
	}
}

func sessionToSummary(sess *session.UISession) debugSessionSummary {
	lastMsgTime := sess.UpdatedAt
	if len(sess.Messages) > 0 {
		lastMsgTime = sess.Messages[len(sess.Messages)-1].CreatedAt
	}
	summary := debugSessionSummary{
		ID:                   sess.ID,
		Title:                sess.Title,
		Status:               string(sess.Status),
		MessageCount:         len(sess.Messages),
		ActiveSkills:         sess.ActiveSkills,
		LastMessageTimestamp: lastMsgTime,
	}
	if sess.Status != session.StatusIdle {
		summary.Run = &runInfo{Status: string(sess.Status)}
	}
	return summary
}

func (s *Server) handleDebugHTTP(w http.ResponseWriter, r *http.Request) {
	if s.config.DebugRecorder == nil {
		writeError(w, http.StatusNotFound, "debug recorder not enabled")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 0
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
			if limit > 100 {
				limit = 100
			}
		}
	}
	sessionID := r.URL.Query().Get("session_id")
	providerID := r.URL.Query().Get("provider_id")

	traces := s.config.DebugRecorder.List(limit, sessionID, providerID)
	inFlight := s.config.DebugRecorder.InFlight()

	result := struct {
		Traces   []*debug.HTTPTrace `json:"traces"`
		InFlight []*debug.HTTPTrace `json:"in_flight"`
	}{
		Traces:   traces,
		InFlight: inFlight,
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDebugHTTPByID(w http.ResponseWriter, r *http.Request) {
	if s.config.DebugRecorder == nil {
		writeError(w, http.StatusNotFound, "debug recorder not enabled")
		return
	}

	id := r.PathValue("trace_id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing trace id")
		return
	}

	trace := s.config.DebugRecorder.Get(debug.TraceID(id))
	if trace == nil {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}

	writeJSON(w, http.StatusOK, trace)
}

// debugUmbrellaResponse is the shape returned by GET /api/debug.
type debugUmbrellaResponse struct {
	Version       string                `json:"version"`
	UpSince       time.Time             `json:"up_since"`
	Runtime       debugRuntimeResponse  `json:"runtime"`
	Sessions      []debugSessionSummary `json:"sessions"`
	HTTPTraces    httpTracesGroup       `json:"http_traces"`
	ConfigSummary *sanitizedConfig      `json:"config_summary"`
}

// httpTracesGroup groups trace lists for the umbrella response.
type httpTracesGroup struct {
	Traces   []*debug.HTTPTrace `json:"traces"`
	InFlight []*debug.HTTPTrace `json:"in_flight"`
}

func (s *Server) handleDebugUmbrella(w http.ResponseWriter, r *http.Request) {
	// Assemble sessions
	allSessions := s.config.SessionManager.All()
	summaries := make([]debugSessionSummary, 0, len(allSessions))
	for _, sess := range allSessions {
		summary := sessionToSummary(sess)
		if summary.Run != nil && s.config.RunService != nil {
			if active := s.config.RunService.ActiveRun(sess.ID); active != nil {
				summary.Run.SSESubscriberCount = active.SSE.SubscriberCount()
				summary.Run.SSEReplayCount = active.SSE.ReplayCount()
			}
		}
		// Enrich with latest HTTP traces
		if s.config.DebugRecorder != nil {
			summary.LatestHTTP = s.config.DebugRecorder.LastN(sess.ID, 3)
		}
		summaries = append(summaries, summary)
	}
	if summaries == nil {
		summaries = []debugSessionSummary{}
	}

	// Assemble runtime
	cfg := s.loadConfig()
	cfgSummary := sanitizeConfig(cfg)

	activeRunCount := 0
	var activeSessions []activeSessionSSEInfo
	if s.config.RunService != nil {
		activeRunCount = s.config.RunService.ActiveRunCount()
		counters := s.config.RunService.ActiveRunSSECounters()
		if len(counters) > 0 {
			activeSessions = make([]activeSessionSSEInfo, 0, len(counters))
			for sessionID, c := range counters {
				activeSessions = append(activeSessions, activeSessionSSEInfo{
					SessionID:          sessionID,
					SSESubscriberCount: c.SubscriberCount,
					SSEReplayCount:     c.ReplayCount,
				})
			}
		}
	}

	recordedTraces := 0
	if s.config.DebugRecorder != nil {
		recordedTraces = s.config.DebugRecorder.Count()
	}

	runtimeResp := debugRuntimeResponse{
		Version:            s.config.Version,
		UpSince:            s.config.StartTime,
		ActiveRunCount:     activeRunCount,
		SessionCount:       s.config.SessionManager.Count(),
		RecordedHTTPTraces: recordedTraces,
		ConfigSummary:      cfgSummary,
		ActiveSessions:     activeSessions,
	}

	// Assemble HTTP traces
	httpTraces := httpTracesGroup{}
	if s.config.DebugRecorder != nil {
		httpTraces.Traces = s.config.DebugRecorder.List(0, "", "")
		httpTraces.InFlight = s.config.DebugRecorder.InFlight()
	}
	if httpTraces.Traces == nil {
		httpTraces.Traces = []*debug.HTTPTrace{}
	}
	if httpTraces.InFlight == nil {
		httpTraces.InFlight = []*debug.HTTPTrace{}
	}

	resp := debugUmbrellaResponse{
		Version:       s.config.Version,
		UpSince:       s.config.StartTime,
		Runtime:       runtimeResp,
		Sessions:      summaries,
		HTTPTraces:    httpTraces,
		ConfigSummary: cfgSummary,
	}

	writeJSON(w, http.StatusOK, resp)
}
