// exchange.go — the exchange-cap sliding window and pending-tool-use repair,
// the two conversation-history behaviours of the canonical session store
// (issue #1239). Both are pure functions over the flat `Message` shape so the
// session store enforces exactly the sliding-window and tool-use-repair
// semantics the loop's LLM boundary expects (umbrella #1231).

package message

import "time"

// DefaultMaxExchanges is the default sliding-window cap in exchanges.
// An exchange begins with a user message and includes all following
// assistant and tool messages until the next user message. It is the single
// canonical default; the session Manager's exchange cap defaults to it
// (issue #1239).
const DefaultMaxExchanges = 150

// TrimExchanges removes the oldest exchanges when the user message count
// exceeds maxExchanges, returning the surviving sliding-window suffix. An
// exchange begins with a user message and includes all following assistant
// and tool messages until the next user message; trimming drops the oldest
// user messages (and everything before each) until the user count is at the
// cap, keeping the assistant/tool tail that follows each trimmed user message
// — the exact sliding-window semantics of the history store's trim. A
// non-positive cap disables trimming. The input slice is not modified.
func TrimExchanges(msgs []Message, maxExchanges int) []Message {
	if maxExchanges <= 0 {
		return msgs
	}

	// Count user messages
	var userCount int
	for _, msg := range msgs {
		if msg.Role == "user" {
			userCount++
		}
	}
	if userCount <= maxExchanges {
		return msgs
	}

	// Need to remove the oldest (userCount - maxExchanges) exchanges
	toRemove := userCount - maxExchanges

	// Find the index of the toRemove-th user message (0-indexed)
	var removeIdx int
	count := 0
	for i, msg := range msgs {
		if msg.Role == "user" {
			count++
			if count == toRemove {
				removeIdx = i
				break
			}
		}
	}

	return msgs[removeIdx+1:]
}

// RepairPendingToolUse returns a copy of messages with any trailing
// unresolved assistant tool call closed by a synthetic tool error result.
//
// A run that is cancelled while a tool is executing can leave the history
// ending in an assistant message with a tool call but no matching tool result.
// Appending a user message directly after that produces an invalid
// OpenAI-style sequence ("user message follows unresolved tool use") which the
// provider hard-rejects. This repairs the dangling tool use so a resume is
// valid. History that does not end in an unresolved assistant tool call is
// returned unchanged.
func RepairPendingToolUse(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	if last.Role != "assistant" {
		return messages
	}
	if len(last.ToolCalls) == 0 {
		return messages
	}

	out := make([]Message, len(messages), len(messages)+1)
	copy(out, messages)

	// A single canceled-out error result is enough to close the pending tool
	// use(s); the LLM sees the agent's own unexecuted call and replies.
	out = append(out, Message{
		Role:       "tool",
		ToolCallID: last.ToolCalls[0].ID,
		Content:    "Tool execution was cancelled before it produced a result.",
		CreatedAt:  time.Now(),
	})
	return out
}
