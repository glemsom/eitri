package report

import (
	"testing"

	"github.com/glemsom/eitri/internal/debug"
)

// TestTurnHasLLMMeta verifies the telemetry-presence predicate for a turn.
// The predicate used to live in the templates package as turnHasLLMMeta;
// per the report-ownership refactor it is report-module behavior tested
// without a browser.
func TestTurnHasLLMMeta(t *testing.T) {
	if (Turn{}).HasLLMMeta() {
		t.Error("empty turn should have no LLM meta")
	}
	if !(Turn{LLMDurationMs: 100}).HasLLMMeta() {
		t.Error("turn with duration should have LLM meta")
	}
	if !(Turn{LLMModel: "gpt-4"}).HasLLMMeta() {
		t.Error("turn with model should have LLM meta")
	}
	if !(Turn{LLMUsage: &debug.UsageTotals{}}).HasLLMMeta() {
		t.Error("turn with usage should have LLM meta")
	}
	if !(Turn{LLMTraceID: "abc"}).HasLLMMeta() {
		t.Error("turn with trace id should have LLM meta")
	}
	if !(Turn{LLMRequestBytes: 1}).HasLLMMeta() {
		t.Error("turn with request bytes should have LLM meta")
	}
	if !(Turn{LLMResponseBytes: 1}).HasLLMMeta() {
		t.Error("turn with response bytes should have LLM meta")
	}
	if !(Turn{LLMAttemptCount: 1}).HasLLMMeta() {
		t.Error("turn with attempt count should have LLM meta")
	}
	if !(Turn{LLMFinishReason: "stop"}).HasLLMMeta() {
		t.Error("turn with finish reason should have LLM meta")
	}
}

// TestContextPercent verifies the context usage percentage helper, moved
// from the templates package.
func TestContextPercent(t *testing.T) {
	if got := ContextPercent(nil); got != 0 {
		t.Errorf("ContextPercent(nil) = %d, want 0", got)
	}
	if got := ContextPercent(&ContextInfo{ContextWindow: 0}); got != 0 {
		t.Errorf("ContextPercent(zero window) = %d, want 0", got)
	}
	if got := ContextPercent(&ContextInfo{TotalTokens: 50000, ContextWindow: 100000}); got != 50 {
		t.Errorf("ContextPercent(50%%) = %d, want 50", got)
	}
	if got := ContextPercent(&ContextInfo{TotalTokens: 150000, ContextWindow: 100000}); got != 100 {
		t.Errorf("ContextPercent(over) = %d, want 100", got)
	}
	if got := ContextPercent(&ContextInfo{TotalTokens: 1234, ContextWindow: 10000}); got != 12 {
		t.Errorf("ContextPercent(truncated) = %d, want 12", got)
	}
}
