package loop

import (
	"github.com/voocel/litellm"
)

// trimMessages removes the oldest message pairs when total non-system messages
// exceed maxHistory. System prompt is always preserved.
// maxHistory of 0 means no limit.
func trimMessages(req *litellm.Request, maxHistory int) {
	if maxHistory <= 0 {
		return
	}

	// Count non-system messages
	nonSysCount := 0
	for _, msg := range req.Messages {
		if string(msg.Role) != "system" {
			nonSysCount++
		}
	}

	if nonSysCount <= maxHistory {
		return
	}

	toRemove := nonSysCount - maxHistory

	// Build new slice preserving system prompt(s) and the most recent messages
	var kept []litellm.Message
	var removed int
	for _, msg := range req.Messages {
		if string(msg.Role) == "system" {
			kept = append(kept, msg)
			continue
		}
		if removed < toRemove {
			removed++
			continue
		}
		kept = append(kept, msg)
	}
	req.Messages = kept
}

// TruncateText truncates s to at most n runes, appending "..." when truncated.
func TruncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
