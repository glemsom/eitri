package templates

import (
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/report"
)

// TurnView holds pre-rendered HTML for a report turn, so the template
// doesn't need to import the api package's markdown renderer.
type TurnView struct {
	Turn              int
	Role              string
	ContentHTML       string // pre-rendered Markdown → HTML
	ReasoningHTML     string // pre-rendered Markdown → HTML
	Timestamp         time.Time
	LLMDurationMs     int64
	LLMTraceID        string
	LLMRequestBytes   int
	LLMResponseBytes  int
	LLMTTFBMs         int64
	LLMTTFTMs         int64
	LLMAttempt        int
	LLMAttemptCount   int
	LLMFailedAttempts int
	LLMModel          string
	LLMFinishReason   string
	LLMUsage          *debug.UsageTotals
	ContextBefore     *report.ContextInfo
	ContextAfter      *report.ContextInfo
	ToolCalls         []report.ToolCallInfo
}

// turnHasLLMMeta reports whether a turn carries any LLM telemetry worth
// rendering in its metrics strip.
func turnHasLLMMeta(turn TurnView) bool {
	return turn.LLMDurationMs > 0 ||
		turn.LLMTraceID != "" ||
		turn.LLMRequestBytes > 0 ||
		turn.LLMResponseBytes > 0 ||
		turn.LLMTTFBMs > 0 ||
		turn.LLMTTFTMs > 0 ||
		turn.LLMAttemptCount > 0 ||
		turn.LLMAttempt > 0 ||
		turn.LLMModel != "" ||
		turn.LLMFinishReason != "" ||
		turn.LLMUsage != nil
}
