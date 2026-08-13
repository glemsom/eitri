package engine

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

// Event is one typed observation from a live run, delivered in stream order to
// an engine subscriber. A consumer (the TUI, a test) switches on the concrete
// Event implementation; the engine pushes exactly the events its run actually
// streams, so downstream panes render without re-parsing the raw stream or the
// internal message history.
//
// All concrete events implement engineEvent (a zero-method marker) so the
// interface is sealed to the engine package.
type Event interface {
	engineEvent()
}

// StreamKind identifies which stream a StreamEvent carries: chain-of-thought
// reasoning (docs/spec.md §6) or assistant answer text. The two are kept apart
// — reasoning is never merged into the answer — so the TUI can render the
// thinking stream as its own collapsible block.
type StreamKind int

const (
	// ReasoningStream is a streamed reasoning_content delta.
	ReasoningStream StreamKind = iota
	// AnswerStream is a streamed assistant answer content delta.
	AnswerStream
)

// StreamEvent is one streamed delta of a turn: a reasoning or answer chunk.
// It is the per-token (per-chunk) building block the TUI grows its in-place
// transcript from; deltas arrive before the turn completes.
type StreamEvent struct {
	// Turn is the zero-based agent-loop turn number (0 for a plain non-tool run).
	Turn int
	// Kind distinguishes reasoning from answer text.
	Kind StreamKind
	// Delta is this chunk's streamed text.
	Delta string
}

// TurnEvent marks a turn boundary: Start fires when a turn begins streaming and
// the matching non-start fires when it completes. EndReason carries the
// terminal finish_reason ("stop", "tool_calls", …) so the TUI can tell an
// ordinary turn from one that handed off to a tool.
type TurnEvent struct {
	Turn      int
	Start     bool
	EndReason string
}

// ToolCallEvent fires when the engine begins to execute a tool call — before
// the call runs, so the TUI can show a "running tool" indicator as the tool
// executes.
type ToolCallEvent struct {
	Turn int
	// ID is the provider-assigned tool_call id this result answers.
	ID string
	// Name is the tool being invoked (e.g. "bash").
	Name string
	// Arguments is the raw JSON arguments string the tool was invoked with.
	Arguments string
}

// ToolResultEvent fires when a tool call's result is available, carrying the
// deterministic compression metadata the TUI needs to render terse tool
// output (docs/spec.md §5): whether the delivered result is the compressed
// form, how many lines it spans, and how many lines were hidden behind the
// explicit "+N more" tail marker. The metadata is derived from the delivered
// result string, so no raw stream or internal history needs re-parsing
// downstream.
type ToolResultEvent struct {
	Turn int
	// ID matches the ToolCallEvent that initiated the call.
	ID string
	// Name is the tool that ran.
	Name string
	// Compressed is true when the delivered result is a truncated/compressed
	// representation (it carries a "+N more" tail marker) rather than raw.
	Compressed bool
	// Lines is the count of lines in the delivered result, including the
	// explicit "+N more" marker line when present.
	Lines int
	// Dropped is the number of content lines hidden behind the "+N more" tail
	// (0 when the result is uncompressed / never truncated).
	Dropped int
}

// UsageEvent carries per-turn token telemetry (docs/spec.md §4): input/output
// tokens and the deepseek prompt-cache hit/miss split that powers the cache
// hit-ratio gauge.
type UsageEvent struct {
	Turn  int
	Usage provider.Usage
}

// CompactedEvent fires when the session is compacted (docs/spec.md §7 /
// ADR-0003): the eviction-and-summary happened between two turns, and the TUI
// surfaces a read-only "[compacted]" marker without blocking the run.
type CompactedEvent struct {
	Turn int
}

// markerRE matches the explicit "+N more" tail marker Eitri's tool-output
// compressor emits when it truncates a heavy listing (docs/spec.md §5): never
// silent, always an explicit count of hidden lines.
var markerRe = regexp.MustCompile(`\+([0-9]+) more\n?$`)

// newToolResultEvent builds a ToolResultEvent from a tool's delivered result,
// deriving the compression metadata deterministically from the result string
// (without re-parsing raw stream or internal history downstream): a result
// carrying the explicit "+N more" tail marker is the compressed form, and the
// marker's count is the number of lines hidden behind it.
func newToolResultEvent(turn int, id, name, result string) ToolResultEvent {
	dropped, lines := 0, 0
	if result != "" {
		lines = strings.Count(result, "\n")
		if !strings.HasSuffix(result, "\n") {
			lines++
		}
		if m := markerRe.FindStringSubmatch(result); m != nil {
			dropped, _ = strconv.Atoi(m[1])
		}
	}
	return ToolResultEvent{
		Turn:       turn,
		ID:         id,
		Name:       name,
		Compressed: dropped > 0,
		Lines:      lines,
		Dropped:    dropped,
	}
}

// sealed Event implementations.
func (StreamEvent) engineEvent()     {}
func (TurnEvent) engineEvent()       {}
func (ToolCallEvent) engineEvent()   {}
func (ToolResultEvent) engineEvent() {}
func (UsageEvent) engineEvent()      {}
func (CompactedEvent) engineEvent()  {}
