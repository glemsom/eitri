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

// ObserveTool stamps ev with the next turn sequence number and appends it to the
// same arrival-ordered log as the streamed text, leaving Content and Reasoning
// untouched so tool events only delimit the text fragments around them.
func (f *TurnFlow) ObserveTool(ev TimelineEvent) {
	ev.Seq = f.turnSeq
	f.turnSeq++
	f.events = append(f.events, ev)
}

func (f *TurnFlow) Events() []TimelineEvent { return f.events }

func (f *TurnFlow) Content() string { return f.content }

func (f *TurnFlow) Reasoning() string { return f.reasoning }

// Finalize reconciles the committed snapshots with the streamed ones: completed
// turns prefer provider-final snapshots when present, while stopped turns keep
// the live partial output if any streamed before stop landed.
func (f *TurnFlow) Finalize(answer, reasoning string, stopped bool) (content, committedReasoning string) {
	return finalizeFlowSnapshot(answer, f.content, stopped), finalizeFlowSnapshot(reasoning, f.reasoning, stopped)
}

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
