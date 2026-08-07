// sync.go — the history→conversation sync seam: converting a run's LLM
// history into the flat message shape used by UI conversations and persisted
// snapshots. This is the documented, unit-tested seam where the two stores
// meet (issue #1235): the loop's history ([]EitriMessage, system prompt
// prepended on reads) becomes the canonical conversation shape
// ([]Message, system prompt stored separately).

package message

// SyncHistoryToConversation converts a run's LLM history into the flat
// conversation message shape, stripping the leading system message (the
// strip-system-message invariant, ADR-0028): the system prompt is stored
// separately (UISession.SystemPrompt / the history manager's system prompt),
// so it must never appear in a persisted facade's Messages list. All run
// transports and manual compaction funnel their UI/snapshot message lists
// through here.
func SyncHistoryToConversation(hist []EitriMessage) []Message {
	msgs := make([]Message, 0, len(hist))
	for _, em := range hist {
		msgs = append(msgs, em.ToMessage())
	}
	return StripLeadingSystemMessage(msgs)
}

// StripLeadingSystemMessage removes the leading system message from a
// conversation message list — the single home of the strip-system-message
// invariant (ADR-0028). A list that does not start with a system message is
// returned unchanged.
func StripLeadingSystemMessage(msgs []Message) []Message {
	if len(msgs) > 0 && msgs[0].Role == "system" {
		return msgs[1:]
	}
	return msgs
}
