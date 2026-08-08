// sync.go — the history→conversation sync seam: converting a run's LLM
// history into the flat message shape used by UI conversations and persisted
// snapshots. This is the documented, unit-tested seam where the two stores
// meet (issue #1235): the loop's history ([]EitriMessage, system prompt
// prepended on reads) becomes the canonical conversation shape
// ([]Message, system prompt stored separately).
//
// Since issue #1241 the loop's session-backed history adapter reads and
// writes the canonical session store directly, so the UI snapshot path no
// longer funnels through this module — it survives for the request-based
// (sub-agent) and batch snapshot facades and for manual compaction until the
// history store is contracted away (umbrella #1231, issue #1242).

package message

// SyncHistoryToConversation converts a run's LLM history into the flat
// conversation message shape, stripping the leading system message (the
// strip-system-message invariant, ADR-0028): the system prompt is stored
// separately (UISession.SystemPrompt / the history manager's system prompt),
// so it must never appear in a persisted facade's Messages list. The
// batch/sub-agent snapshot facade (buildUISession) and manual compaction
// funnel their UI/snapshot message lists through here.
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
