package tui

// phase.go is the derived "what is the agent doing right now" seam (issue
// #363): a single Phase enum computed from the live turn state — the busy flag
// plus what the in-progress assistant stream is doing — instead of scattered
// boolean checks across the render paths. It is the prefactor the stage-label
// verb (#365) and the live-reasoning panel (#364) hang off. No visible change:
// the busy indicator keeps today's "working" verb for every active phase.

// Phase is what the agent is doing right now, derived from the live turn state
// rather than stored.
type Phase int

const (
	// PhaseIdle: no turn runs.
	PhaseIdle Phase = iota
	// PhaseReasoning: a turn runs and chain-of-thought is streaming (before
	// the answer begins).
	PhaseReasoning
	// PhaseWorking: a turn runs but no assistant text streams yet — the
	// tool-heavy gap (or pre/post-stream silence).
	PhaseWorking
	// PhaseAnswering: a turn runs and answer text is streaming.
	PhaseAnswering
)

// String is the stable wire/display name of a phase, used in logs and tests.
func (p Phase) String() string {
	switch p {
	case PhaseReasoning:
		return "reasoning"
	case PhaseWorking:
		return "working"
	case PhaseAnswering:
		return "answering"
	default:
		return "idle"
	}
}

// derivePhase is the pure phase derivation from the bare live-turn signals:
// whether a turn is running (busy) and whether/which assistant text is
// streaming. It returns idle when no turn runs; reasoning while only
// chain-of-thought grows; answering once answer text flows (answer takes
// precedence over trailing reasoning); working in the busy gap where nothing
// streams yet.
func derivePhase(busy, streaming, hasReasoning, hasAnswer bool) Phase {
	if !busy {
		return PhaseIdle
	}
	if streaming {
		if hasAnswer {
			return PhaseAnswering
		}
		if hasReasoning {
			return PhaseReasoning
		}
	}
	return PhaseWorking
}

// phase returns the agent's current Phase, derived from the owned live turn
// state: the busy flag and the in-progress streaming assistant message (if
// any). The transcript and status strip read this instead of re-deriving
// boolean checks scattered across render paths, so the stage labels and the
// live-reasoning panel hang off one seam.
func (t Transcript) phase() Phase {
	var streaming, hasReasoning, hasAnswer bool
	for i := len(t.messages) - 1; i >= 0; i-- {
		if m := t.messages[i]; m.streaming {
			streaming = true
			hasReasoning = m.reasoning != ""
			hasAnswer = m.content != ""
			break
		}
	}
	return derivePhase(t.busy, streaming, hasReasoning, hasAnswer)
}

// phaseVerb maps a busy Phase to the status verb rendered under the busy
// spinner. Issue #363 deliberately keeps every active phase on the single
// "working" verb so the surface is unchanged; issue #365 splits it into
// Reasoning / Working / Answering here, branching on p. Today the parameter is
// the seam rather than a driver — every phase says "working".
func phaseVerb(p Phase) string {
	_ = p // label split lands in #365
	return "working"
}
