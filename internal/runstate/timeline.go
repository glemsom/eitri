package runstate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// TerminationReason describes why a run ended.
type TerminationReason string

const (
	TerminationCompleted TerminationReason = "completed"
	TerminationMaxTurns  TerminationReason = "max_turns"
	TerminationCancelled TerminationReason = "cancelled"
	TerminationError     TerminationReason = "error"
)

// TimelineEvent represents one condensed SSE event in the persisted timeline.
// High-volume events (token, thinking_delta) are excluded.
type TimelineEvent struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Turn      int       `json:"turn"`

	// Tool call/result
	Tool   string `json:"tool,omitempty"`
	Args   any    `json:"args,omitempty"`
	Output string `json:"output,omitempty"`
	Error  bool   `json:"error,omitempty"`

	// Context update
	TotalTokens   int `json:"total_tokens,omitempty"`
	PromptTokens  int `json:"prompt_tokens,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`
	// Actual provider token usage (if available from LLM response)
	ActualPromptTokens     int `json:"actual_prompt_tokens,omitempty"`
	ActualCompletionTokens int `json:"actual_completion_tokens,omitempty"`

	// Component
	Name string `json:"name,omitempty"`
	Data any    `json:"data,omitempty"`

	// Skill activated
	SkillName string `json:"skill_name,omitempty"`

	// Needs confirmation
	ConfirmPath    string `json:"confirm_path,omitempty"`
	ConfirmMessage string `json:"confirm_message,omitempty"`

	// Done / Error
	Message   string      `json:"message,omitempty"`
	MessageID string      `json:"message_id,omitempty"`
	Usage     *TokenUsage `json:"usage,omitempty"`

	// LLM call correlation (llm_call events): the HTTP trace ID of the
	// successful attempt plus retry count and timing, so the session report
	// can join this turn to its traces by ID.
	TraceID    string `json:"trace_id,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	TTFBMs     int64  `json:"ttfb_ms,omitempty"`
	TTFTMs     int64  `json:"ttft_ms,omitempty"`
}

// TimelineTermination describes why the run ended.
type TimelineTermination struct {
	Reason  TerminationReason `json:"reason"`
	Message string            `json:"message"`
}

// TimelineProvider identifies the LLM provider used for this run.
type TimelineProvider struct {
	Model      string `json:"model"`
	ProviderID string `json:"provider_id"`
}

// Timeline is the persisted timeline file format (one per run).
type Timeline struct {
	Version     int                  `json:"version"`
	RunID       string               `json:"run_id"`
	SessionID   string               `json:"session_id"`
	Provider    TimelineProvider     `json:"provider"`
	StartedAt   time.Time            `json:"started_at"`
	EndedAt     time.Time            `json:"ended_at"`
	Termination *TimelineTermination `json:"termination,omitempty"`
	Events      []TimelineEvent      `json:"events"`
}

// shouldExcludeFromTimeline returns true for high-volume event types that
// should not be included in the condensed timeline.
func shouldExcludeFromTimeline(evt SSEEvent) bool {
	return evt.Type == "token" || evt.Type == "thinking_delta"
}

// CondensedEvents filters the SSE history to only semantic events (excludes
// token and thinking_delta events). Returns a copy.
func (s *State) CondensedEvents() []TimelineEvent {
	history := s.History()
	events := make([]TimelineEvent, 0, len(history))

	var turn int

	for _, evt := range history {
		if shouldExcludeFromTimeline(evt) {
			continue
		}

		timelineEvt := TimelineEvent{
			Type:      evt.Type,
			Timestamp: evt.Timestamp,
			Turn:      evt.Turn,
		}

		// If Turn is 0 (not explicitly set), infer it from the event sequence.
		if evt.Turn == 0 {
			timelineEvt.Turn = turn
		} else {
			turn = evt.Turn
		}

		switch evt.Type {
		case "tool_call":
			timelineEvt.Tool = evt.Tool
			timelineEvt.Args = evt.Args

		case "tool_result":
			timelineEvt.Tool = evt.Tool
			if output, ok := evt.Output.(string); ok {
				timelineEvt.Output = output
				// Tool dispatch errors are prefixed with "Tool error:"
				timelineEvt.Error = strings.HasPrefix(output, "Tool error:")
			}

		case "context_update":
			if cu, ok := evt.Data.(*ContextUpdate); ok {
				timelineEvt.TotalTokens = cu.TotalTokens
				timelineEvt.PromptTokens = cu.PromptTokens
				timelineEvt.ContextWindow = cu.ContextWindow
				timelineEvt.ActualPromptTokens = cu.ActualPromptTokens
				timelineEvt.ActualCompletionTokens = cu.ActualCompletionTokens
			}

		case "component":
			timelineEvt.Data = evt.Data
			timelineEvt.Name = evt.Name

		case "llm_call":
			if li, ok := evt.Data.(*LLMCallInfo); ok {
				timelineEvt.TraceID = li.TraceID
				timelineEvt.Attempt = li.Attempt
				timelineEvt.Attempts = li.Attempts
				timelineEvt.DurationMs = li.DurationMs
				timelineEvt.TTFBMs = li.TTFBMs
				timelineEvt.TTFTMs = li.TTFTMs
			}

		case "skill_activated":
			timelineEvt.SkillName = evt.Tool

		case "needs_confirmation":
			if data, ok := evt.Data.(map[string]any); ok {
				timelineEvt.ConfirmPath, _ = data["path"].(string)
				timelineEvt.ConfirmMessage, _ = data["message"].(string)
			}
			timelineEvt.Message = evt.Content

		case "done":
			timelineEvt.MessageID = evt.MessageID
			timelineEvt.Usage = evt.Usage

		case "error":
			timelineEvt.Message = evt.Message
		}

		events = append(events, timelineEvt)
	}

	return events
}

// GenerateRunID creates a short hex run identifier from session ID and start time.
func GenerateRunID(sessionID string, startedAt time.Time) string {
	h := sha256.Sum256([]byte(sessionID + startedAt.Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:8])
}
