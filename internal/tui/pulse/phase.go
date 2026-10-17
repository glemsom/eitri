package pulse

// Phase is what the agent is doing right now, derived from the live turn state rather than stored.
type Phase int

const (
	PhaseIdle Phase = iota
	PhaseReasoning
	PhaseWorking
	PhaseAnswering
)

// String is the stable wire name of a phase, used in logs and tests; the user-facing stage label rendered under the busy spinner is Verb.
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

// Verb is the user-facing stage label rendered beside the busy spinner.
func Verb(p Phase) string {
	switch p {
	case PhaseReasoning:
		return "Reasoning"
	case PhaseAnswering:
		return "Answering"
	default:
		return "Working"
	}
}

// derivePhase is the pure phase derivation from the bare live-turn signals: whether a turn is running (busy) and whether/which assistant text is streaming.
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
