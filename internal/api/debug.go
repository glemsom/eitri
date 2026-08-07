package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/session"
)

// debugSessionSummary is the shape returned by GET /api/debug/sessions.
type debugSessionSummary struct {
	ID                   string             `json:"id"`
	Title                string             `json:"title"`
	Status               string             `json:"status"`
	MessageCount         int                `json:"message_count"`
	ActiveSkills         []string           `json:"active_skills"`
	Run                  *runInfo           `json:"run,omitempty"`
	LatestHTTP           []*debug.HTTPTrace `json:"latest_http"`
	LastMessageTimestamp time.Time          `json:"last_message_timestamp"`
}

type runInfo struct {
	Status             string `json:"status"`
	Busy               bool   `json:"busy"`
	Turns              int    `json:"turns"`
	PendingApproval    bool   `json:"pending_approval"`
	SSESubscriberCount uint64 `json:"sse_subscriber_count"`
	SSEReplayCount     uint64 `json:"sse_replay_count"`
}

// debugSessionDetail is the shape returned by GET /api/debug/sessions/{id}.
type debugSessionDetail struct {
	Session      debugSessionSummary `json:"session"`
	Messages     []message.Message   `json:"messages"`
	ActiveSkills []string            `json:"active_skills"`
	Run          *runInfo            `json:"run,omitempty"`
	SSEHistory   []runstate.SSEEvent `json:"sse_history,omitempty"`
}

// debugRuntimeResponse is the shape returned by GET /api/debug/runtime.
type debugRuntimeResponse struct {
	Version            string                 `json:"version"`
	UpSince            time.Time              `json:"up_since"`
	ActiveRunCount     int                    `json:"active_run_count"`
	SessionCount       int                    `json:"session_count"`
	RecordedHTTPTraces int                    `json:"recorded_http_traces"`
	ConfigSummary      *sanitizedConfig       `json:"config_summary"`
	ActiveSessions     []activeSessionSSEInfo `json:"active_sessions,omitempty"`
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
	MaxOutputTokens     int    `json:"max_output_tokens"`
	CommandTimeout      int64  `json:"command_timeout"`
	TurnTimeout         int64  `json:"turn_timeout"`
	HasAPIKey           bool   `json:"has_api_key"`
}

// writeJSON is a helper to write a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
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
		summary := sessionToSummaryFromID(s.config.SessionManager, sess.ID)
		if summary == nil {
			continue
		}
		// Enrich with run info if run active — snapshot under RunService.mu
		if summary.Run != nil && s.config.RunService != nil {
			if snap := s.config.RunService.ActiveRunSSESnapshot(sess.ID); snap != nil {
				summary.Run.Busy = snap.Busy
				summary.Run.Turns = snap.Turns
				summary.Run.PendingApproval = snap.PendingApproval
				summary.Run.SSESubscriberCount = snap.SubscriberCount
				summary.Run.SSEReplayCount = snap.ReplayCount
			}
		}
		// Enrich with latest HTTP traces
		if s.config.DebugRecorder != nil {
			summary.LatestHTTP = s.config.DebugRecorder.LastN(sess.ID, 3)
		}
		summaries = append(summaries, *summary)
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

	// Keeps the copy helpers: this endpoint is a troubleshooting state dump
	// that is polled concurrently with active agent runs. The run loop mutates
	// session state in place (e.g. meta.UpdatedAt during run setup, message
	// content on turn completion), so a shared reference would race with it.
	// A detached copy gives a consistent point-in-time snapshot.
	meta := s.config.SessionManager.CopyMeta(id)
	if meta == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	convo := s.config.SessionManager.CopyConversation(id)

	// Support ?limit_messages=N
	msgCount := len(convo.Messages)
	if limitStr := r.URL.Query().Get("limit_messages"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n >= 0 && n < msgCount {
			msgCount = n
		}
	}

	messages := convo.Messages
	if msgCount < len(messages) {
		messages = messages[len(messages)-msgCount:]
	}
	if messages == nil {
		messages = []message.Message{}
	}

	detail := debugSessionDetail{
		Session:      sessionToSummary(meta, convo),
		Messages:     messages,
		ActiveSkills: convo.ActiveSkills,
	}
	// Enrich with latest HTTP traces
	if s.config.DebugRecorder != nil {
		detail.Session.LatestHTTP = s.config.DebugRecorder.LastN(id, 3)
	}
	if meta.Status != session.StatusIdle {
		detail.Run = &runInfo{Status: string(meta.Status)}
		if s.config.RunService != nil {
			if snap := s.config.RunService.ActiveRunSSESnapshot(id); snap != nil {
				detail.Run.Busy = snap.Busy
				detail.Run.Turns = snap.Turns
				detail.Run.PendingApproval = snap.PendingApproval
				detail.Run.SSESubscriberCount = snap.SubscriberCount
				detail.Run.SSEReplayCount = snap.ReplayCount
				detail.SSEHistory = snap.History
			}
		}
	}

	writeJSON(w, http.StatusOK, detail)
}

// handleDebugSessionHTTP handles GET /api/debug/sessions/{id}/http
// Returns HTTP traces filtered to the given session.
func (s *Server) handleDebugSessionHTTP(w http.ResponseWriter, r *http.Request) {
	if s.config.DebugRecorder == nil {
		writeError(w, http.StatusNotFound, "debug recorder not enabled")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}

	// The in-memory endpoint reads a bounded ring buffer: no limit parameter
	// means "return everything the recorder holds" (0 = no limit) and the cap
	// is the in-memory ceiling, not the archive's. Parsing goes through the
	// shared trace-filter parser (issue #1240); model/time/offset parameters
	// are accepted and ignored, mirroring the persisted endpoints' surface.
	filter, errMsg := parseTraceFilter(r, 0, maxInMemoryTraceLimit)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	traces := s.config.DebugRecorder.List(filter.Limit, sessionID, filter.ProviderID)
	inFlight := s.config.DebugRecorder.InFlight()

	writeJSON(w, http.StatusOK, struct {
		Traces   []*debug.HTTPTrace `json:"traces"`
		InFlight []*debug.HTTPTrace `json:"in_flight"`
	}{
		Traces:   traces,
		InFlight: inFlight,
	})
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

// handleDebugMetrics handles GET /api/debug/metrics: per-provider-per-model
// interaction counters (calls, retries, errors by class, latency histogram,
// token totals, cache hit/miss) accumulated by the trace recorder.
func (s *Server) handleDebugMetrics(w http.ResponseWriter, r *http.Request) {
	if s.config.DebugRecorder == nil {
		writeError(w, http.StatusNotFound, "debug recorder not enabled")
		return
	}
	writeJSON(w, http.StatusOK, s.config.DebugRecorder.Metrics())
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
		MaxOutputTokens:     cfg.MaxOutputTokens,
		CommandTimeout:      cfg.CommandTimeout,
		TurnTimeout:         cfg.TurnTimeout,
		HasAPIKey:           cfg.APIKey != "" || len(cfg.ProviderAuth) > 0,
	}
}

func sessionToSummary(meta *session.SessionMeta, convo *session.Conversation) debugSessionSummary {
	lastMsgTime := meta.UpdatedAt
	if len(convo.Messages) > 0 {
		lastMsgTime = convo.Messages[len(convo.Messages)-1].CreatedAt
	}
	summary := debugSessionSummary{
		ID:                   meta.ID,
		Title:                meta.Title,
		Status:               string(meta.Status),
		MessageCount:         len(convo.Messages),
		ActiveSkills:         convo.ActiveSkills,
		LastMessageTimestamp: lastMsgTime,
	}
	if meta.Status != session.StatusIdle {
		summary.Run = &runInfo{Status: string(meta.Status)}
	}
	return summary
}

// sessionToSummaryFromID builds a debug summary from the copy helpers. The
// debug endpoints run concurrently with active agent runs whose in-place
// mutations (meta.UpdatedAt, message content) would race with shared references,
// so they intentionally stay on the detached-copy path (see handleDebugSessionByID).
func sessionToSummaryFromID(mgr *session.Manager, id string) *debugSessionSummary {
	meta := mgr.CopyMeta(id)
	if meta == nil {
		return nil
	}
	convo := mgr.CopyConversation(id)
	summary := sessionToSummary(meta, convo)
	return &summary
}

func (s *Server) handleDebugHTTP(w http.ResponseWriter, r *http.Request) {
	if s.config.DebugRecorder == nil {
		writeError(w, http.StatusNotFound, "debug recorder not enabled")
		return
	}

	// The in-memory endpoint reads a bounded ring buffer: no limit parameter
	// means "return everything the recorder holds" (0 = no limit) and the cap
	// is the in-memory ceiling, not the archive's. Parsing goes through the
	// shared trace-filter parser (issue #1240); model/time/offset parameters
	// are accepted and ignored, mirroring the persisted endpoints' surface.
	filter, errMsg := parseTraceFilter(r, 0, maxInMemoryTraceLimit)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	traces := s.config.DebugRecorder.List(filter.Limit, filter.SessionID, filter.ProviderID)
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

// maxInMemoryTraceLimit caps the limit parameter of the in-memory trace
// endpoints (/api/debug/http and /api/debug/sessions/{id}/http), which read a
// bounded ring buffer.
const maxInMemoryTraceLimit = 100

// maxPersistedTraceLimit caps the limit parameter of the persisted-trace
// query endpoints. The in-memory endpoints cap at 100 because they read a
// bounded ring buffer; the persisted archive can be much larger, so callers
// get more headroom (pagination via offset covers the rest).
const maxPersistedTraceLimit = 1000

// parseTraceFilter parses the shared query parameters of the trace endpoints:
// session_id, provider_id, model, from, to, limit and offset. defaultLimit is
// applied when no limit parameter is present (0 = no limit); maxLimit caps it.
// Returns a 400-style error message when a parameter is malformed. Every
// debug trace endpoint — the persisted archive query, its aggregate, and the
// in-memory recorder lists — parses its filters through this one function
// (issue #1240).
func parseTraceFilter(r *http.Request, defaultLimit, maxLimit int) (persist.TraceFilter, string) {
	q := r.URL.Query()
	filter := persist.TraceFilter{
		SessionID:  q.Get("session_id"),
		ProviderID: q.Get("provider_id"),
		Model:      q.Get("model"),
	}
	for _, field := range []struct {
		name string
		dst  *time.Time
	}{
		{"from", &filter.From},
		{"to", &filter.To},
	} {
		if v := q.Get(field.name); v != "" {
			ts, err := parseTraceTime(v)
			if err != nil {
				return persist.TraceFilter{}, "invalid " + field.name + ": " + err.Error()
			}
			*field.dst = ts
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return persist.TraceFilter{}, "invalid limit: expected a non-negative integer"
		}
		if n > maxLimit {
			n = maxLimit
		}
		filter.Limit = n
	} else {
		filter.Limit = defaultLimit
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return persist.TraceFilter{}, "invalid offset: expected a non-negative integer"
		}
		filter.Offset = n
	}
	return filter, ""
}

// parseTraceTime parses a query-parameter timestamp. RFC3339 with optional
// fractional seconds is accepted (RFC3339Nano's layout also parses plain
// RFC3339 timestamps).
func parseTraceTime(v string) (time.Time, error) {
	ts, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 timestamp (e.g. 2026-01-02T15:04:05Z): %q", v)
	}
	return ts, nil
}

// handleDebugTraces handles GET /api/debug/traces: query the persisted trace
// archive (on disk) by session_id / provider_id / model / time range, with
// limit/offset pagination. Unlike /api/debug/http, this reads the full
// historical archive — not just the restored in-memory ring buffer.
func (s *Server) handleDebugTraces(w http.ResponseWriter, r *http.Request) {
	if s.config.Persister == nil {
		writeError(w, http.StatusNotFound, "persisted trace archive not available")
		return
	}

	filter, errMsg := parseTraceFilter(r, 20, maxPersistedTraceLimit)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	page, err := s.config.Persister.QueryTraces(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query persisted traces: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// traceAggregateResponse is the shape returned by GET /api/debug/traces/aggregate.
type traceAggregateResponse struct {
	GeneratedAt time.Time `json:"generated_at"`
	persist.TraceAggregate
}

// handleDebugTracesAggregate handles GET /api/debug/traces/aggregate: window
// aggregates (count, error rate, p50/p95 latency, token totals) over the
// persisted trace archive using the same filters as /api/debug/traces.
func (s *Server) handleDebugTracesAggregate(w http.ResponseWriter, r *http.Request) {
	if s.config.Persister == nil {
		writeError(w, http.StatusNotFound, "persisted trace archive not available")
		return
	}

	filter, errMsg := parseTraceFilter(r, 20, maxPersistedTraceLimit)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	// The aggregate is a window summary — pagination parameters are ignored.
	filter.Limit = 0
	filter.Offset = 0

	agg, err := s.config.Persister.AggregateTraces(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to aggregate persisted traces: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, traceAggregateResponse{GeneratedAt: time.Now(), TraceAggregate: *agg})
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
		summary := sessionToSummaryFromID(s.config.SessionManager, sess.ID)
		if summary == nil {
			continue
		}
		if summary.Run != nil && s.config.RunService != nil {
			if snap := s.config.RunService.ActiveRunSSESnapshot(sess.ID); snap != nil {
				summary.Run.SSESubscriberCount = snap.SubscriberCount
				summary.Run.SSEReplayCount = snap.ReplayCount
			}
		}
		// Enrich with latest HTTP traces
		if s.config.DebugRecorder != nil {
			summary.LatestHTTP = s.config.DebugRecorder.LastN(sess.ID, 3)
		}
		summaries = append(summaries, *summary)
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
