package engine

import (
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

// Event is one typed observation from a live run, delivered in stream order to an engine subscriber.
type Event interface {
	engineEvent()
}

// StreamKind identifies which stream a StreamEvent carries: chain-of-thought reasoning or assistant answer text.
type StreamKind int

const (
	ReasoningStream StreamKind = iota
	AnswerStream
)

// StreamEvent is one streamed delta of a turn: a reasoning or answer chunk.
type StreamEvent struct {
	RunID int
	Turn  int
	Kind  StreamKind
	Delta string
}

// TurnEvent marks a turn boundary: Start fires when a turn begins streaming and the matching non-start fires when it completes.
type TurnEvent struct {
	RunID     int
	Turn      int
	Start     bool
	EndReason string
}

// ToolCallEvent fires when the engine begins to execute a tool call — before the call runs, so the TUI can show a "running tool" indicator as the tool executes.
type ToolCallEvent struct {
	RunID     int
	Turn      int
	ID        string
	Name      string
	Arguments string
}

// ToolResultEvent fires when a tool call's result is available, carrying the deterministic compression metadata the TUI needs to render terse tool output: whether the result is the compressed form, how many lines it spans, and how many lines were hidden behind the explicit "+N more" tail marker.
type ToolResultEvent struct {
	RunID        int
	Turn         int
	ID           string
	Name         string
	Result       string
	BytesDropped int
	Compressed   bool
	Lines        int
	Dropped      int
}

// UsageEvent carries per-turn token telemetry: input/output tokens and the deepseek prompt-cache hit/miss split that powers the cache hit-ratio gauge.
type UsageEvent struct {
	RunID int
	Turn  int
	Usage provider.Usage
}

// addUsage folds one turn's usage into the run total. The total stays nil until
// a turn reports usage, so a run with no reported usage has no total at all
// rather than a zero-valued one.
func addUsage(total, turn *provider.Usage) *provider.Usage {
	if turn == nil {
		return total
	}
	if total == nil {
		total = &provider.Usage{}
	}
	total.PromptTokens += turn.PromptTokens
	total.CompletionTokens += turn.CompletionTokens
	total.PromptCacheHitTokens += turn.PromptCacheHitTokens
	total.PromptCacheMissTokens += turn.PromptCacheMissTokens
	total.PromptCacheWriteTokens += turn.PromptCacheWriteTokens
	return total
}

// CompactedEvent fires when context overflow recovery summarized or evicted older history before retrying the provider request.
type CompactedEvent struct {
	RunID int
	Turn  int
}

func newToolResultEvent(runID, turn int, id, name string, result ToolExecResult, bytesDropped int) ToolResultEvent {
	lines := 0
	if result.Text != "" {
		lines = strings.Count(result.Text, "\n")
		if !strings.HasSuffix(result.Text, "\n") {
			lines++
		}
	}
	return ToolResultEvent{
		RunID: runID, Turn: turn, ID: id, Name: name, Result: result.Text,
		BytesDropped: bytesDropped, Compressed: result.Compressed,
		Lines: lines, Dropped: result.Dropped,
	}
}

// sealed Event implementations.
func (StreamEvent) engineEvent()     {}
func (TurnEvent) engineEvent()       {}
func (ToolCallEvent) engineEvent()   {}
func (ToolResultEvent) engineEvent() {}
func (UsageEvent) engineEvent()      {}
func (CompactedEvent) engineEvent()  {}
