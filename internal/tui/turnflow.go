package tui

import "strings"

// TurnFlow records one turn's streamed reasoning/answer observations in order
// and exposes the live snapshots derived from them. Commit-time finalization can
// reconcile provider final snapshots with what streamed without dropping a
// stopped turn's visible partial output.
type TurnFlow struct {
	events    []TimelineEvent
	content   string
	reasoning string
	turnSeq   int
}

// Observe records one streamed text observation. Empty deltas are ignored.
func (f *TurnFlow) Observe(kind StreamKind, delta string) bool {
	if delta == "" {
		return false
	}
	ev := TimelineEvent{Kind: streamEventKind(kind), Seq: f.turnSeq, Delta: delta}
	f.turnSeq++
	f.events = append(f.events, ev)
	if kind == ReasoningStream {
		f.reasoning += delta
	} else {
		f.content += delta
	}
	return true
}

// ObserveTool records one tool start/result observation in the same
// arrival-ordered event log as the streamed reasoning/answer observations,
// stamping it with the next turn sequence number. Tool observations carry no
// text snapshot of their own, so Content and Reasoning stay untouched; they
// only delimit the reasoning/answer fragments around them in the log.
func (f *TurnFlow) ObserveTool(ev TimelineEvent) {
	ev.Seq = f.turnSeq
	f.turnSeq++
	f.events = append(f.events, ev)
}

// Events returns the ordered streamed text observations.
func (f *TurnFlow) Events() []TimelineEvent { return f.events }

// Content returns the live answer snapshot derived from observed deltas.
func (f *TurnFlow) Content() string { return f.content }

// Reasoning returns the live reasoning snapshot derived from observed deltas.
func (f *TurnFlow) Reasoning() string { return f.reasoning }

// Finalize reconciles the committed snapshots with the streamed ones: completed
// turns prefer provider-final snapshots when present, while stopped turns keep
// the live partial output if any streamed before stop landed.
func (f *TurnFlow) Finalize(answer, reasoning string, stopped bool) (content, committedReasoning string) {
	return finalizeFlowSnapshot(answer, f.content, stopped), finalizeFlowSnapshot(reasoning, f.reasoning, stopped)
}

// Reset clears the flow for the next turn.
func (f *TurnFlow) Reset() {
	f.events = nil
	f.content = ""
	f.reasoning = ""
	f.turnSeq = 0
}

func finalizeFlowSnapshot(final, live string, stopped bool) string {
	if stopped {
		switch {
		case final == "":
			return live
		case live == "":
			return final
		case len(final) >= len(live) && strings.HasPrefix(final, live):
			return final
		default:
			return live
		}
	}
	if final != "" {
		return final
	}
	return live
}
