// sync.go — the history→conversation sync seam: converting a run's LLM
// history into the flat message shape used by snapshot facades. This is the
// documented, unit-tested seam where the two stores meet (issue #1235): the
// loop's history ([]EitriMessage, system prompt prepended on reads) becomes
// the canonical conversation shape ([]Message, system prompt stored
// separately).
//
// Since issue #1241 the loop's session-backed history adapter reads and
// writes the canonical session store directly, so the UI live-sync and manual
// compaction (CompactSession) no longer funnel through this module — they
// operate on the canonical store through the same adapter. The seam survives
// for exactly one call site, the batch/sub-agent snapshot facade
// (buildUISession), until the history store is contracted away (umbrella
// #1231, issue #1242).

package message

// SyncHistoryToConversation converts a run's LLM history into the flat
// conversation message shape, stripping the leading system message (the
// strip-system-message invariant, ADR-0028): the system prompt is stored
// separately (UISession.SystemPrompt / the history manager's system prompt),
// so it must never appear in a persisted facade's Messages list. The
// batch/sub-agent snapshot facade (buildUISession) funnels its message list
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
