// Package report provides the Session Report assembly service.
// It reads persisted data (timeline, session snapshots, LLM history, HTTP traces)
// and assembles a structured Session Report for the UI.
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/session"
)

// TerminationInfo describes why a run ended.
type TerminationInfo struct {
	Reason  runstate.TerminationReason `json:"reason"`
	Message string                     `json:"message"`
}

// RunInfo is a summary of one run in a session.
type RunInfo struct {
	Run         int             `json:"run"`
	StartedAt   time.Time       `json:"started_at"`
	Turns       int             `json:"turns"`
	Termination TerminationInfo `json:"termination"`
}

// ContextInfo holds token usage for context updates.
type ContextInfo struct {
	TotalTokens            int `json:"total_tokens"`
	PromptTokens           int `json:"prompt_tokens"`
	ContextWindow          int `json:"context_window"`
	ActualPromptTokens     int `json:"actual_prompt_tokens,omitempty"`
	ActualCompletionTokens int `json:"actual_completion_tokens,omitempty"`
}

// ToolCallInfo represents one tool call in a turn.
type ToolCallInfo struct {
	Name            string `json:"name"`
	Arguments       any    `json:"arguments"`
	ResultPreview   string `json:"result_preview"`
	ResultTruncated bool   `json:"result_truncated"`
	Error           bool   `json:"error"`
	DurationMs      int64  `json:"duration_ms,omitempty"`
}

// Turn represents one turn in the assistant run.
type Turn struct {
	Turn             int       `json:"turn"`
	Role             string    `json:"role"`
	Content          string    `json:"content"`
	ReasoningContent string    `json:"reasoning_content,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
	LLMDurationMs    int64     `json:"llm_duration_ms,omitempty"`
	LLMTraceID       string    `json:"llm_trace_id,omitempty"`
	LLMRequestBytes  int       `json:"llm_request_bytes,omitempty"`
	LLMResponseBytes int       `json:"llm_response_bytes,omitempty"`
	// Enriched per-call measurements from the matched HTTP trace.
	LLMTTFBMs       int64              `json:"llm_ttfb_ms,omitempty"`
	LLMAttempt      int                `json:"llm_attempt,omitempty"`
	LLMModel        string             `json:"llm_model,omitempty"`
	LLMFinishReason string             `json:"llm_finish_reason,omitempty"`
	LLMUsage        *debug.UsageTotals `json:"llm_usage,omitempty"`
	ContextBefore   *ContextInfo       `json:"context_before,omitempty"`
	ContextAfter    *ContextInfo       `json:"context_after,omitempty"`
	ToolCalls       []ToolCallInfo     `json:"tool_calls,omitempty"`
}

// Summary holds aggregate statistics for a run.
type Summary struct {
	TotalTurns                int      `json:"total_turns"`
	TotalLLMCalls             int      `json:"total_llm_calls"`
	TotalToolCalls            int      `json:"total_tool_calls"`
	FailedToolCalls           int      `json:"failed_tool_calls"`
	FailedToolNames           []string `json:"failed_tool_names,omitempty"`
	HallucinatedTools         []string `json:"hallucinated_tools,omitempty"`
	EstimatedTotalTokens      int      `json:"estimated_total_tokens"`
	EstimatedCompletionTokens int      `json:"estimated_completion_tokens"`
	TotalPromptTokens         int      `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens     int      `json:"total_completion_tokens,omitempty"`
	TotalCacheReadTokens      int      `json:"total_cache_read_tokens,omitempty"`
	TotalCacheWriteTokens     int      `json:"total_cache_write_tokens,omitempty"`
	TotalDurationMs           int64    `json:"total_duration_ms"`
	Note                      string   `json:"note,omitempty"`
}

// SessionReport is the complete report for one run of a session.
type SessionReport struct {
	SessionID     string           `json:"session_id"`
	Title         string           `json:"title"`
	SystemPrompt  string           `json:"system_prompt,omitempty"`
	Model         string           `json:"model"`
	Provider      string           `json:"provider"`
	Workspace     string           `json:"workspace"`
	StartedAt     time.Time        `json:"started_at"`
	EndedAt       time.Time        `json:"ended_at"`
	DurationMs    int64            `json:"duration_ms"`
	ReportVersion string           `json:"report_version"` // "full" or "reconstructed"
	Termination   *TerminationInfo `json:"termination,omitempty"`
	Turns         []Turn           `json:"turns"`
	Summary       Summary          `json:"summary"`
	SubAgents     []string         `json:"sub_agents,omitempty"`
}

// Service assembles Session Reports from on-disk data.
type Service struct {
	persister *persist.Persister
}

// New creates a new report Service.
func New(persister *persist.Persister) *Service {
	return &Service{persister: persister}
}

// maxResultPreviewLength is the max chars for tool result preview.
const maxResultPreviewLength = 500

// ListRuns returns metadata for all runs in a session.
func (svc *Service) ListRuns(sessionID string) ([]RunInfo, error) {
	metas, err := svc.persister.ListTimelines(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list timelines: %w", err)
	}
	if len(metas) == 0 {
		// No timeline — try reconstructed from history
		return svc.listRunsFromHistory(sessionID)
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].StartedAt.Before(metas[j].StartedAt)
	})

	runs := make([]RunInfo, 0, len(metas))
	for i, meta := range metas {
		data, err := svc.persister.LoadTimeline(sessionID, meta.Filename)
		if err != nil {
			continue
		}
		var tl struct {
			Termination *runstate.TimelineTermination `json:"termination"`
			Events      []runstate.TimelineEvent      `json:"events"`
		}
		if err := json.Unmarshal(data, &tl); err != nil {
			continue
		}

		turnCount := 0
		for _, evt := range tl.Events {
			if evt.Turn > turnCount {
				turnCount = evt.Turn
			}
		}

		reason := runstate.TerminationCompleted
		message := ""
		if tl.Termination != nil {
			reason = tl.Termination.Reason
			message = tl.Termination.Message
		}

		runs = append(runs, RunInfo{
			Run:         i,
			StartedAt:   meta.StartedAt,
			Turns:       turnCount,
			Termination: TerminationInfo{Reason: reason, Message: message},
		})
	}
	return runs, nil
}

// listRunsFromHistory reconstructs a best-effort run list from the session snapshot.
func (svc *Service) listRunsFromHistory(sessionID string) ([]RunInfo, error) {
	data, err := svc.persister.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if data == nil {
		return nil, nil
	}

	// Single reconstructed run
	return []RunInfo{
		{
			Run:       0,
			StartedAt: time.Now(),
			Turns:     0,
			Termination: TerminationInfo{
				Reason:  runstate.TerminationCompleted,
				Message: "",
			},
		},
	}, nil
}

// GetReport assembles the full Session Report for a specific run.
// runIndex identifies which run (0-based index by start time).
func (svc *Service) GetReport(sessionID string, runIndex int) (*SessionReport, error) {
	metas, err := svc.persister.ListTimelines(sessionID)
	if err != nil {
		return nil, fmt.Errorf("list timelines: %w", err)
	}

	if len(metas) == 0 {
		return svc.buildReconstructedReport(sessionID)
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].StartedAt.Before(metas[j].StartedAt)
	})

	if runIndex < 0 || runIndex >= len(metas) {
		return nil, fmt.Errorf("run index %d out of range (0-%d)", runIndex, len(metas)-1)
	}

	meta := metas[runIndex]
	data, err := svc.persister.LoadTimeline(sessionID, meta.Filename)
	if err != nil {
		return nil, fmt.Errorf("load timeline: %w", err)
	}

	var tl runstate.Timeline
	if err := json.Unmarshal(data, &tl); err != nil {
		return nil, fmt.Errorf("unmarshal timeline: %w", err)
	}

	// Load session snapshot
	report := svc.buildReportFromTimeline(sessionID, &tl, metas)

	// Try to enrich with session snapshot data
	report = svc.enrichFromSnapshot(sessionID, report)

	// Try to enrich with trace data
	report = svc.enrichFromTraces(sessionID, report)

	return report, nil
}

// buildReportFromTimeline assembles a report from timeline data.
func (svc *Service) buildReportFromTimeline(sessionID string, tl *runstate.Timeline, allMetas []persist.TimelineMeta) *SessionReport {
	report := &SessionReport{
		SessionID:     sessionID,
		Model:         tl.Provider.Model,
		Provider:      tl.Provider.ProviderID,
		StartedAt:     tl.StartedAt,
		EndedAt:       tl.EndedAt,
		DurationMs:    tl.EndedAt.Sub(tl.StartedAt).Milliseconds(),
		ReportVersion: "full",
		SubAgents:     []string{},
	}

	if tl.Termination != nil {
		report.Termination = &TerminationInfo{
			Reason:  tl.Termination.Reason,
			Message: tl.Termination.Message,
		}
	}

	// Build turns from events
	turnsMap := make(map[int]*Turn)
	var lastContextBefore *ContextInfo

	for _, evt := range tl.Events {
		turn := evt.Turn
		if turn == 0 {
			continue
		}

		if _, ok := turnsMap[turn]; !ok {
			turnsMap[turn] = &Turn{
				Turn:      turn,
				Timestamp: evt.Timestamp,
			}
		}
		t := turnsMap[turn]

		switch evt.Type {
		case "tool_call":
			t.Role = "assistant"
			t.ToolCalls = append(t.ToolCalls, ToolCallInfo{
				Name:      evt.Tool,
				Arguments: evt.Args,
				Error:     false,
			})
		case "tool_result":
			// Find the last tool call in this turn and attach result
			for i := len(t.ToolCalls) - 1; i >= 0; i-- {
				if t.ToolCalls[i].Name == evt.Tool && t.ToolCalls[i].ResultPreview == "" {
					t.ToolCalls[i].ResultPreview = truncatePreview(evt.Output)
					t.ToolCalls[i].ResultTruncated = len(evt.Output) > maxResultPreviewLength
					t.ToolCalls[i].Error = evt.Error
					break
				}
			}
		case "context_update":
			ci := &ContextInfo{
				TotalTokens:            evt.TotalTokens,
				PromptTokens:           evt.PromptTokens,
				ContextWindow:          evt.ContextWindow,
				ActualPromptTokens:     evt.ActualPromptTokens,
				ActualCompletionTokens: evt.ActualCompletionTokens,
			}
			if lastContextBefore != nil {
				t.ContextBefore = lastContextBefore
			}
			t.ContextAfter = ci
			lastContextBefore = ci
		}
	}

	// Convert to sorted slice
	turns := make([]Turn, 0, len(turnsMap))
	for _, t := range turnsMap {
		turns = append(turns, *t)
	}
	sort.Slice(turns, func(i, j int) bool {
		return turns[i].Turn < turns[j].Turn
	})

	// Interleave user messages — since timeline only captures tool calls,
	// we insert placeholder user messages at turn boundaries.
	// The session snapshot will fill in real content.
	finalTurns := make([]Turn, 0, len(turns)*2)
	for _, t := range turns {
		finalTurns = append(finalTurns, Turn{
			Turn:      t.Turn,
			Role:      "user",
			Timestamp: t.Timestamp,
		})
		finalTurns = append(finalTurns, t)
	}

	report.Turns = finalTurns

	// Build summary
	report.Summary = svc.computeSummary(tl, turns)

	return report
}

// computeSummary calculates aggregate statistics from a timeline.
func (svc *Service) computeSummary(tl *runstate.Timeline, turns []Turn) Summary {
	summary := Summary{
		TotalTurns:      len(turns),
		TotalDurationMs: tl.EndedAt.Sub(tl.StartedAt).Milliseconds(),
	}

	toolCallCount := 0
	failedCount := 0
	failedNames := make(map[string]int)
	hallucinatedNames := make(map[string]int)
	totalTokens := 0

	for _, t := range turns {
		for _, tc := range t.ToolCalls {
			toolCallCount++
			if tc.Error {
				failedCount++
				failedNames[tc.Name]++
			}
		}
	}

	// Compute estimated tokens from context updates
	for _, evt := range tl.Events {
		if evt.Type == "context_update" && evt.TotalTokens > totalTokens {
			totalTokens = evt.TotalTokens
		}
	}

	// Compute total actual usage across all assistant turns
	totalPromptTokens := 0
	totalCompletionTokens := 0
	for _, t := range turns {
		if t.Role == "assistant" && t.ContextAfter != nil {
			totalPromptTokens += t.ContextAfter.ActualPromptTokens
			totalCompletionTokens += t.ContextAfter.ActualCompletionTokens
		}
	}

	summary.TotalToolCalls = toolCallCount
	summary.FailedToolCalls = failedCount
	summary.TotalLLMCalls = len(turns)
	summary.EstimatedTotalTokens = totalTokens
	summary.TotalPromptTokens = totalPromptTokens
	summary.TotalCompletionTokens = totalCompletionTokens

	for name := range failedNames {
		summary.FailedToolNames = append(summary.FailedToolNames, name)
	}
	sort.Strings(summary.FailedToolNames)

	for name := range hallucinatedNames {
		summary.HallucinatedTools = append(summary.HallucinatedTools, name)
	}
	sort.Strings(summary.HallucinatedTools)

	return summary
}

// enrichFromSnapshot fills in titles, user messages, and reasoning content from session snapshot.
func (svc *Service) enrichFromSnapshot(sessionID string, report *SessionReport) *SessionReport {
	data, err := svc.persister.LoadSession(sessionID)
	if err != nil || data == nil {
		return report
	}

	var snap session.UISession
	if err := json.Unmarshal(data, &snap); err != nil {
		return report
	}

	report.Title = snap.Title
	report.Workspace = snap.Workspace
	report.SystemPrompt = snap.SystemPrompt

	// Map messages into turns by looking at timestamps
	msgIdx := 0
	for i, t := range report.Turns {
		if t.Role == "user" {
			// Find the next user message from snapshot
			for msgIdx < len(snap.Messages) && snap.Messages[msgIdx].Role != "user" {
				msgIdx++
			}
			if msgIdx < len(snap.Messages) {
				report.Turns[i].Content = snap.Messages[msgIdx].Content
				report.Turns[i].Timestamp = snap.Messages[msgIdx].CreatedAt
				msgIdx++
			}
		} else if t.Role == "assistant" {
			// Match assistant message content and reasoning
			for msgIdx < len(snap.Messages) && snap.Messages[msgIdx].Role != "assistant" {
				msgIdx++
			}
			if msgIdx < len(snap.Messages) {
				report.Turns[i].Content = snap.Messages[msgIdx].Content
				report.Turns[i].ReasoningContent = snap.Messages[msgIdx].ReasoningContent
				msgIdx++
			}
		}
	}

	// Sub-agents: look for delegate/collect tool results mentioning sub-agent IDs
	for _, turn := range report.Turns {
		for _, tc := range turn.ToolCalls {
			if tc.Name == "delegate" || tc.Name == "collect" {
				// Extract sub-agent session ID from arguments
				if args, ok := tc.Arguments.(map[string]any); ok {
					if id, ok := args["session_id"].(string); ok && id != "" {
						if !contains(report.SubAgents, id) {
							report.SubAgents = append(report.SubAgents, id)
						}
					}
				}
			}
		}
	}

	return report
}

// enrichFromTraces fills in LLM timing, bytes, and per-call measurements
// (usage, finish reason, model, attempt, TTFB) from the HTTP traces recorded
// for the run. It also updates the summary cache-token totals.
func (svc *Service) enrichFromTraces(sessionID string, report *SessionReport) *SessionReport {
	traceIDs, err := svc.persister.ListTraces(sessionID)
	if err != nil || len(traceIDs) == 0 {
		return report
	}

	type matchedTrace struct {
		durationMs    int64
		requestBytes  int
		responseBytes int
		traceID       string
		ttfbMs        int64
		attempt       int
		model         string
		finishReason  string
		usage         *debug.UsageTotals
		timeDiff      time.Duration
	}

	for i, turn := range report.Turns {
		if turn.Role != "assistant" {
			continue
		}

		var best *matchedTrace
		for _, tid := range traceIDs {
			data, err := svc.persister.LoadTrace(sessionID, tid)
			if err != nil {
				continue
			}
			var trace debug.HTTPTrace
			if err := json.Unmarshal(data, &trace); err != nil {
				continue
			}

			diff := trace.Timestamp.Sub(turn.Timestamp)
			if diff < 0 {
				diff = -diff
			}
			if diff > 30*time.Second {
				continue
			}
			if best == nil || diff < best.timeDiff {
				best = &matchedTrace{
					durationMs:    trace.DurationMs,
					requestBytes:  trace.RequestBytes,
					responseBytes: trace.ResponseBytes,
					traceID:       string(trace.ID),
					ttfbMs:        trace.TTFBMs,
					attempt:       trace.Attempt,
					model:         trace.Model,
					finishReason:  trace.FinishReason,
					usage:         trace.Usage,
					timeDiff:      diff,
				}
			}
		}

		if best != nil {
			report.Turns[i].LLMDurationMs = best.durationMs
			report.Turns[i].LLMRequestBytes = best.requestBytes
			report.Turns[i].LLMResponseBytes = best.responseBytes
			report.Turns[i].LLMTraceID = best.traceID
			report.Turns[i].LLMTTFBMs = best.ttfbMs
			report.Turns[i].LLMAttempt = best.attempt
			report.Turns[i].LLMModel = best.model
			report.Turns[i].LLMFinishReason = best.finishReason
			report.Turns[i].LLMUsage = best.usage
		}
	}

	// Update cache-token totals from the enriched turns.
	var cacheRead, cacheWrite int
	for _, t := range report.Turns {
		if t.LLMUsage != nil {
			cacheRead += t.LLMUsage.CacheReadTokens
			cacheWrite += t.LLMUsage.CacheWriteTokens
		}
	}
	if cacheRead > 0 || cacheWrite > 0 {
		report.Summary.TotalCacheReadTokens = cacheRead
		report.Summary.TotalCacheWriteTokens = cacheWrite
	}

	return report
}

// buildReconstructedReport builds a report from session snapshot only (no timeline).
func (svc *Service) buildReconstructedReport(sessionID string) (*SessionReport, error) {
	data, err := svc.persister.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	var snapData []byte
	if data != nil {
		snapData = data
	}

	var title, workspace string
	var snap session.UISession
	if snapData != nil {
		if err := json.Unmarshal(snapData, &snap); err == nil {
			title = snap.Title
			workspace = snap.Workspace
		}
	}

	report := &SessionReport{
		SessionID:     sessionID,
		Title:         title,
		Workspace:     workspace,
		ReportVersion: "reconstructed",
		DurationMs:    0,
		SubAgents:     []string{},
		Summary: Summary{
			Note: "limited data — no timeline persisted for this session",
		},
	}

	if snapData != nil && len(snap.Messages) > 0 {
		turnNum := 0
		for _, msg := range snap.Messages {
			turnNum++
			t := Turn{
				Turn:             turnNum,
				Role:             msg.Role,
				Content:          msg.Content,
				ReasoningContent: msg.ReasoningContent,
				Timestamp:        msg.CreatedAt,
			}
			// Extract tool calls from assistant messages
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					var args any
					if tc.Function.Arguments != "" {
						json.Unmarshal([]byte(tc.Function.Arguments), &args)
					}
					t.ToolCalls = append(t.ToolCalls, ToolCallInfo{
						Name:      tc.Function.Name,
						Arguments: args,
					})
				}
			}
			report.Turns = append(report.Turns, t)
			report.Summary.TotalTurns++
			report.Summary.TotalLLMCalls++
		}
	}

	if snapData != nil {
		report = svc.enrichFromSnapshot(sessionID, report)
	}

	return report, nil
}

// truncatePreview truncates a string to maxResultPreviewLength chars.
func truncatePreview(s string) string {
	if len(s) <= maxResultPreviewLength {
		return s
	}
	return s[:maxResultPreviewLength] + "..."
}

// contains checks if a slice contains a string.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
