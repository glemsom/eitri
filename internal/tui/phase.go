package tui

// phase.go is the derived "what is the agent doing right now" seam (issue
// #363): a single Phase enum computed from the live turn state — the busy flag
// plus what the in-progress assistant stream is doing — instead of scattered
// boolean checks across the render paths. It is the prefactor the stage-label
// verb (#365) and the live-reasoning panel (#364) hang off. Issue #365 splits
// the busy indicator's verb by stage: Reasoning while chain-of-thought streams,
// Working while tools run, Answering while answer text flows.

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

// String is the stable wire name of a phase, used in logs and tests; the
// user-facing stage label rendered under the busy spinner is phaseVerb.
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
// spinner (issue #365): Reasoning while chain-of-thought streams, Working in
// the tool-heavy gap (or pre/post-stream silence), Answering once answer text
// flows. Idle never renders through the busy line, so it falls back to the
// busy verb rather than surfacing "idle" mid-turn.
func phaseVerb(p Phase) string {
	switch p {
	case PhaseReasoning:
		return "Reasoning"
	case PhaseAnswering:
		return "Answering"
	default:
		return "Working"
	}
}
