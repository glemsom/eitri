// Package report provides the Session Report assembly service.
// It reads persisted data (timeline, session snapshots, LLM history, HTTP traces)
// and assembles a structured Session Report for the UI.
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/timeline"
)

// TerminationInfo describes why a run ended.
type TerminationInfo struct {
	Reason  timeline.TerminationReason `json:"reason"`
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
	LLMTTFBMs         int64              `json:"llm_ttfb_ms,omitempty"`
	LLMTTFTMs         int64              `json:"llm_ttft_ms,omitempty"`
	LLMAttempt        int                `json:"llm_attempt,omitempty"`
	LLMAttemptCount   int                `json:"llm_attempt_count,omitempty"`
	LLMFailedAttempts int                `json:"llm_failed_attempts,omitempty"`
	LLMModel          string             `json:"llm_model,omitempty"`
	LLMFinishReason   string             `json:"llm_finish_reason,omitempty"`
	LLMUsage          *debug.UsageTotals `json:"llm_usage,omitempty"`
	ContextBefore     *ContextInfo       `json:"context_before,omitempty"`
	ContextAfter      *ContextInfo       `json:"context_after,omitempty"`
	ToolCalls         []ToolCallInfo     `json:"tool_calls,omitempty"`
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
	TotalRetries              int      `json:"total_retries,omitempty"`
	TotalDurationMs           int64    `json:"total_duration_ms"`
	Note                      string   `json:"note,omitempty"`
}

// SessionReport is the complete report for one run of a session.
type SessionReport struct {
	SessionID     string           `json:"session_id"`
	RunID         string           `json:"run_id,omitempty"`
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
		tl, err := svc.persister.LoadTimeline(sessionID, meta.Filename)
		if err != nil {
			continue
		}

		turnCount := 0
		for _, evt := range tl.Events {
			if evt.Turn > turnCount {
				turnCount = evt.Turn
			}
		}

		reason := timeline.TerminationCompleted
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
	snap, err := svc.persister.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if snap == nil {
		return nil, nil
	}

	// Single reconstructed run
	return []RunInfo{
		{
			Run:       0,
			StartedAt: time.Now(),
			Turns:     0,
			Termination: TerminationInfo{
				Reason:  timeline.TerminationCompleted,
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
	tl, err := svc.persister.LoadTimeline(sessionID, meta.Filename)
	if err != nil {
		return nil, fmt.Errorf("load timeline: %w", err)
	}

	// Load session snapshot
	report := svc.buildReportFromTimeline(sessionID, tl, metas)

	// Try to enrich with session snapshot data
	report = svc.enrichFromSnapshot(sessionID, report)

	// Try to enrich with trace data
	report = svc.enrichFromTraces(sessionID, tl.RunID, report)

	return report, nil
}

// buildReportFromTimeline assembles a report from timeline data.
func (svc *Service) buildReportFromTimeline(sessionID string, tl *timeline.Timeline, allMetas []persist.TimelineMeta) *SessionReport {
	report := &SessionReport{
		SessionID:     sessionID,
		RunID:         tl.RunID,
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

	// Build turns from events. turnsOrder records the first-seen emission
	// sequence of turn numbers so the report can order cards by timeline
	// emission order instead of re-sorting by turn number (issue #1158): turn
	// numbering and emission order can diverge after compaction, trimmed
	// history, or sub-agent runs.
	turnsMap := make(map[int]*Turn)
	turnsOrder := make([]int, 0)
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
			turnsOrder = append(turnsOrder, turn)
		}
		t := turnsMap[turn]

		switch evt.Type {
		case "llm_call":
			// The timeline records the trace ID of this turn's successful LLM
			// call plus its retry count and timing at write time, so the report
			// joins turn → trace by ID without any timestamp heuristic.
			t.Role = "assistant"
			t.LLMTraceID = evt.TraceID
			t.LLMAttempt = evt.Attempt
			t.LLMAttemptCount = evt.Attempts
			t.LLMDurationMs = evt.DurationMs
			t.LLMTTFBMs = evt.TTFBMs
			t.LLMTTFTMs = evt.TTFTMs
			// The last attempt of a turn's successful call succeeded, so the
			// failures are every attempt before it.
			if evt.Attempts > 0 {
				t.LLMFailedAttempts = evt.Attempts - 1
			}
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

	// Convert to a slice in timeline emission order (first-seen sequence),
	// rather than sorting by turn number.
	turns := make([]Turn, 0, len(turnsOrder))
	for _, turn := range turnsOrder {
		turns = append(turns, *turnsMap[turn])
	}

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
func (svc *Service) computeSummary(tl *timeline.Timeline, turns []Turn) Summary {
	summary := Summary{
		TotalTurns:      len(turns),
		TotalDurationMs: tl.EndedAt.Sub(tl.StartedAt).Milliseconds(),
	}

	toolCallCount := 0
	failedCount := 0
	failedNames := make(map[string]int)
	hallucinatedNames := make(map[string]int)
	totalTokens := 0
	totalRetries := 0

	for _, t := range turns {
		for _, tc := range t.ToolCalls {
			toolCallCount++
			if tc.Error {
				failedCount++
				failedNames[tc.Name]++
			}
		}
		totalRetries += t.LLMFailedAttempts
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
	summary.TotalRetries = totalRetries

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
	snap, err := svc.persister.LoadSession(sessionID)
	if err != nil || snap == nil {
		return report
	}

	report.Title = snap.Title
	report.Workspace = snap.Workspace
	report.SystemPrompt = snap.SystemPrompt

	// Collect the user-role placeholder cards and the assistant turns in report
	// order, so a user message can be attributed to the turn that follows it in
	// chronological terms by timestamp (issue #1159). Pulling user content with a
	// single advancing index across the whole snapshot misattributes messages and
	// corrupts displayed timestamps whenever snapshot order and turn order
	// diverge (e.g. after compaction, sub-agent runs, or trimmed history).
	userCardIdx := make([]int, 0, len(report.Turns))
	turnTimes := make([]time.Time, 0, len(report.Turns))
	for i, t := range report.Turns {
		switch t.Role {
		case "user":
			userCardIdx = append(userCardIdx, i)
		case "assistant":
			turnTimes = append(turnTimes, t.Timestamp)
		}
	}

	// Extract the user messages from the snapshot, preserving their array
	// order. Snapshot array order is the tie-break when a user message and its
	// turn share (or invert) timestamps.
	var userMsgs []message.Message
	// Assistant messages are matched independently below, preserving the prior
	// sequential behavior without a shared cursor corrupting the user match.
	var assistantMsgs []*message.Message
	for i := range snap.Messages {
		m := &snap.Messages[i]
		if m.Role == "user" {
			userMsgs = append(userMsgs, *m)
		} else if m.Role == "assistant" {
			assistantMsgs = append(assistantMsgs, m)
		}
	}

	// Attribute each user message (in array order) to the earliest assistant
	// turn whose emitted time is at/after the message's created_at. When no
	// such turn is left (inverted timestamps, or every later turn already
	// matched), fall back to the earliest unassigned turn's card — which is the
	// snapshot array-order tie-break required by the acceptance criteria.
	matched := make([]bool, len(turnTimes))
	for _, um := range userMsgs {
		slot := -1
		for j, ts := range turnTimes {
			if !matched[j] && !ts.Before(um.CreatedAt) {
				slot = j
				break
			}
		}
		if slot == -1 {
			// Fallback: earliest unassigned turn (array-order tie-break).
			for j := range turnTimes {
				if !matched[j] {
					slot = j
					break
				}
			}
		}
		if slot == -1 {
			break
		}
		matched[slot] = true
		ci := userCardIdx[slot]
		report.Turns[ci].Content = um.Content
		report.Turns[ci].Timestamp = um.CreatedAt
	}

	// Assistant content and reasoning are matched independently, in snapshot
	// array order, onto assistant cards in report order. This decouples the
	// assistant match from the user attribution so a divergence in one role's
	// order never shifts the other (the prior shared cursor allowed that).
	astSlot := 0
	for i, t := range report.Turns {
		if t.Role != "assistant" {
			continue
		}
		if astSlot < len(assistantMsgs) {
			report.Turns[i].Content = assistantMsgs[astSlot].Content
			report.Turns[i].ReasoningContent = assistantMsgs[astSlot].ReasoningContent
			astSlot++
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

	// Drop empty placeholder user cards. buildReportFromTimeline inserts a
	// synthetic user card before every assistant turn; when no real user
	// message could be attributed to a turn (e.g. after compaction, or a turn
	// with no preceding prompt), that card stays empty and renders as noise.
	// Remove such cards so only user messages with actual matched content
	// render (issue #1160).
	turns := make([]Turn, 0, len(report.Turns))
	for _, t := range report.Turns {
		if t.Role == "user" && t.Content == "" {
			continue
		}
		turns = append(turns, t)
	}
	report.Turns = turns

	return report
}

// enrichFromTraces fills in LLM timing, bytes, and per-call measurements
// (usage, finish reason, model, attempt, TTFB, TTFT) from the HTTP traces
// recorded for the run. It also updates the summary cache-token totals.
//
// Turns are joined to traces by ID (issue #988): the timeline records the
// trace ID of each turn's LLM call, and every trace records its run and turn
// IDs. When a turn has no recorded ID (e.g. timelines persisted before the
// feature), the join falls back to grouping traces by (run, turn) and finally
// to the legacy ±30s timestamp heuristic.
func (svc *Service) enrichFromTraces(sessionID, runID string, report *SessionReport) *SessionReport {
	// Load all traces for the session once.
	traces, err := svc.persister.ListTraces(sessionID)
	if err != nil || len(traces) == 0 {
		return report
	}

	// Index traces by ID for direct joins.
	byID := make(map[string]*debug.HTTPTrace, len(traces))
	for _, tr := range traces {
		byID[string(tr.ID)] = tr
	}

	// Group traces by (run, turn): retries of one turn produce multiple traces
	// sharing the same run/turn, which both counts attempts and acts as a
	// fallback join when the timeline lacks an explicit trace ID.
	byRunTurn := make(map[string][]*debug.HTTPTrace)
	for _, tr := range traces {
		key := runTurnKey(tr.RunID, tr.Turn)
		byRunTurn[key] = append(byRunTurn[key], tr)
	}

	for i, turn := range report.Turns {
		if turn.Role != "assistant" {
			continue
		}

		var best *debug.HTTPTrace

		// Primary: join by the trace ID recorded on the turn/timeline.
		if turn.LLMTraceID != "" {
			best = byID[turn.LLMTraceID]
		}

		// Fallback: join by (run, turn), preferring the highest attempt (the
		// final attempt of the turn).
		if best == nil && runID != "" && turn.Turn > 0 {
			for _, tr := range byRunTurn[runTurnKey(runID, turn.Turn)] {
				if best == nil || tr.Attempt > best.Attempt {
					best = tr
				}
			}
		}

		// Last resort: legacy ±30s timestamp proximity heuristic for data
		// persisted without correlation IDs.
		if best == nil {
			best = closestTraceByTimestamp(traces, turn.Timestamp)
		}

		if best != nil {
			report.Turns[i].LLMTraceID = string(best.ID)
			report.Turns[i].LLMDurationMs = best.DurationMs
			report.Turns[i].LLMRequestBytes = best.RequestBytes
			report.Turns[i].LLMResponseBytes = best.ResponseBytes
			report.Turns[i].LLMTTFBMs = best.TTFBMs
			report.Turns[i].LLMTTFTMs = best.TTFTMs
			report.Turns[i].LLMAttempt = best.Attempt
			report.Turns[i].LLMModel = best.Model
			report.Turns[i].LLMFinishReason = best.FinishReason
			report.Turns[i].LLMUsage = best.Usage
		}

		// Attempt count and failures: prefer the values recorded on the
		// timeline (they count every attempt the loop made, even when a
		// failed trace was later pruned); otherwise derive them from the
		// (run, turn) trace group.
		if report.Turns[i].LLMAttemptCount == 0 && runID != "" && turn.Turn > 0 {
			if group := byRunTurn[runTurnKey(runID, turn.Turn)]; len(group) > 0 {
				report.Turns[i].LLMAttemptCount = len(group)
			}
		}
		if report.Turns[i].LLMFailedAttempts == 0 && runID != "" && turn.Turn > 0 {
			if group := byRunTurn[runTurnKey(runID, turn.Turn)]; len(group) > 0 {
				failed := 0
				for _, tr := range group {
					if !isTraceSuccess(tr) {
						failed++
					}
				}
				// Every attempt before the final one was a retry; use that
				// count when the trace outcomes don't mark them explicitly.
				if failed == 0 && len(group) > 1 {
					failed = len(group) - 1
				}
				if failed > 0 {
					report.Turns[i].LLMFailedAttempts = failed
				}
			}
		}
	}

	// Recompute the retry aggregate from the enriched turns (the timeline
	// values may have been refined by trace outcomes above).
	var totalRetries int
	for _, t := range report.Turns {
		totalRetries += t.LLMFailedAttempts
	}
	report.Summary.TotalRetries = totalRetries

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

// isTraceSuccess reports whether a trace represents a successful LLM call
// (HTTP 2xx and no transport error).
func isTraceSuccess(tr *debug.HTTPTrace) bool {
	return tr.Error == "" && tr.Status >= 200 && tr.Status < 300
}

// runTurnKey builds the grouping key for traces by run and turn.
func runTurnKey(runID string, turn int) string {
	return runID + "\x00" + strconv.Itoa(turn)
}

// closestTraceByTimestamp returns the trace closest to ts within a ±30s
// window, or nil when none qualify. This is the legacy heuristic kept as a
// fallback for data persisted without correlation IDs.
func closestTraceByTimestamp(traces []*debug.HTTPTrace, ts time.Time) *debug.HTTPTrace {
	var best *debug.HTTPTrace
	var bestDiff time.Duration
	const window = 30 * time.Second
	for _, tr := range traces {
		diff := tr.Timestamp.Sub(ts)
		if diff < 0 {
			diff = -diff
		}
		if diff > window {
			continue
		}
		if best == nil || diff < bestDiff {
			best = tr
			bestDiff = diff
		}
	}
	return best
}

// buildReconstructedReport builds a report from session snapshot only (no timeline).
func (svc *Service) buildReconstructedReport(sessionID string) (*SessionReport, error) {
	snap, err := svc.persister.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	var title, workspace string
	if snap != nil {
		title = snap.Title
		workspace = snap.Workspace
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

	if snap != nil && len(snap.Messages) > 0 {
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

	if snap != nil {
		report = svc.enrichFromSnapshot(sessionID, report)
	}
	// No timeline exists for this session, so there is no run ID to group
	// traces by; enrichment falls back to the timestamp heuristic.
	report = svc.enrichFromTraces(sessionID, "", report)

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
