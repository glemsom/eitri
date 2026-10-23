package tui

import "strings"

// EventKind identifies the kind of one entry in a per-turn event timeline: the four shapes of activity a turn emits in arrival order.
type EventKind int

const (
	EventReasoning EventKind = iota
	EventToolStart
	EventToolResult
	EventAnswer
)

func (k EventKind) String() string {
	switch k {
	case EventReasoning:
		return "reasoning"
	case EventToolStart:
		return "toolStart"
	case EventToolResult:
		return "toolResult"
	case EventAnswer:
		return "answer"
	}
	return "unknown"
}

// TimelineEvent is one arrival-ordered entry in a per-turn event timeline. The
// payload lives in the field matching Kind: Delta for reasoning/answer deltas,
// Start for a tool start, Result for a tool result. Seq is the event's arrival
// index within its turn's log (0-based), assigned when the event is recorded.
type TimelineEvent struct {
	Kind   EventKind
	Seq    int
	Delta  string
	Start  *ToolStart
	Result *ToolResult
}

// deriveSnapshots folds an event log into the content/reasoning text snapshots
// in arrival order, so the log alone reproduces what the turn streamed: these
// are the derived forms copy-to-clipboard, telemetry, and the gateway export
// keep reading after the timeline becomes the source of truth.
func deriveSnapshots(events []TimelineEvent) (content, reasoning string) {
	var cb, rb strings.Builder
	for _, ev := range events {
		switch ev.Kind {
		case EventAnswer:
			cb.WriteString(ev.Delta)
		case EventReasoning:
			rb.WriteString(ev.Delta)
		}
	}
	return cb.String(), rb.String()
}

func streamEventKind(kind StreamKind) EventKind {
	if kind == ReasoningStream {
		return EventReasoning
	}
	return EventAnswer
}

func toolEventKind(u ToolUpdate) (EventKind, bool) {
	switch {
	case u.Start != nil:
		return EventToolStart, true
	case u.Result != nil:
		return EventToolResult, true
	}
	return 0, false
}
