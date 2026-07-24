package runner

import (
	"github.com/glemsom/eitri/internal/llm"
)

// trimMessages removes the oldest message pairs when total non-system messages
// exceed maxHistory. System prompt is always preserved.
// maxHistory of 0 means no limit.
func trimMessages(req *llm.Request, maxHistory int) {
	if maxHistory <= 0 {
		return
	}

	// Count non-system messages
	nonSysCount := 0
	for _, msg := range req.Messages {
		if msg.Role != "system" {
			nonSysCount++
		}
	}

	if nonSysCount <= maxHistory {
		return
	}

	toRemove := nonSysCount - maxHistory

	// Build new slice preserving system prompt(s) and the most recent messages
	var kept []llm.Message
	var removed int
	for _, msg := range req.Messages {
		if msg.Role == "system" {
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

// truncateText truncates s to at most n runes, appending "..." when truncated.
func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
