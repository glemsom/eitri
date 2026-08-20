package engine

import (
	"regexp"
	"strconv"
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
	Turn  int
	Kind  StreamKind
	Delta string
}

// TurnEvent marks a turn boundary: Start fires when a turn begins streaming and the matching non-start fires when it completes.
type TurnEvent struct {
	Turn      int
	Start     bool
	EndReason string
}

// ToolCallEvent fires when the engine begins to execute a tool call — before the call runs, so the TUI can show a "running tool" indicator as the tool executes.
type ToolCallEvent struct {
	Turn      int
	ID        string
	Name      string
	Arguments string
}

// ToolResultEvent fires when a tool call's result is available, carrying the deterministic compression metadata the TUI needs to render terse tool output: whether the result is the compressed form, how many lines it spans, and how many lines were hidden behind the explicit "+N more" tail marker.
type ToolResultEvent struct {
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
	Turn  int
	Usage provider.Usage
}

// CompactedEvent fires when the session is compacted: the eviction-and-summary happened between two turns, and the TUI surfaces a read-only "[compacted]" marker without blocking the run.
type CompactedEvent struct {
	Turn int
}

// markerRe matches an explicit truncation tail marker Eitri emits when it truncates a heavy tool result — never silent, always an explicit count of what was hidden.
var markerRe = regexp.MustCompile(`\+([0-9]+) more(?:, \+[0-9]+ bytes truncated)?\n?$`)

// newToolResultEvent builds a ToolResultEvent from a tool's full (pre-cap) result, deriving the compression metadata deterministically from the result string (without re-parsing raw stream or internal history downstream): a result carrying the explicit "+N more" tail marker is the compressed form, and the marker's count is the number of lines hidden behind it. bytesDropped is the byte-cap split: the bytes the cap dropped (the capped form lives only in the provider Message; the event carries Result full).
func newToolResultEvent(turn int, id, name, result string, bytesDropped int) ToolResultEvent {
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
		Turn:         turn,
		ID:           id,
		Name:         name,
		Result:       result,
		BytesDropped: bytesDropped,
		Compressed:   dropped > 0,
		Lines:        lines,
		Dropped:      dropped,
	}
}

// sealed Event implementations.
func (StreamEvent) engineEvent()     {}
func (TurnEvent) engineEvent()       {}
func (ToolCallEvent) engineEvent()   {}
func (ToolResultEvent) engineEvent() {}
func (UsageEvent) engineEvent()      {}
func (CompactedEvent) engineEvent()  {}
