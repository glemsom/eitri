// Package uixt provides user-facing string helpers for agent-run outcomes.
// It owns the human-friendly error and max-turn messages that render
// identically in the UI and batch output, so changing "how an error reads"
// does not require touching SSE fan-out code. It is network-agnostic.
package uixt

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// Pre-compiled regexes for HTML tag stripping
	htmlTagRe          = regexp.MustCompile(`<[^>]*>`)
	whitespaceRe       = regexp.MustCompile(`\s+`)
	scriptTagContentRe = regexp.MustCompile(`(?i)<script[^>]*>[\s\S]*?</script>`)
	styleTagContentRe  = regexp.MustCompile(`(?i)<style[^>]*>[\s\S]*?</style>`)
)

// FormatErrorMessage converts ADK/provider errors to user-friendly messages.
func FormatErrorMessage(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Connection refused: LLM provider is not reachable. Check that your provider is running."
	case strings.Contains(msg, "401") || strings.Contains(msg, "Authentication"):
		return "Authentication failed. Check your API key in Settings."
	case strings.Contains(msg, "429") || strings.Contains(msg, "Rate limit"):
		return "Rate limited by provider. Please wait a moment and try again."
	case strings.Contains(msg, "context length") || strings.Contains(msg, "maximum context"):
		return "Context length exceeded. Try a shorter message or reduce conversation history."
	case strings.Contains(msg, "model no longer available") || strings.Contains(msg, "model not found"):
		return "Selected model no longer available. Choose another model in Settings."
	case strings.Contains(msg, "streaming tool calls") || strings.Contains(msg, "streaming not supported"):
		return "Provider does not support required streaming tool calls. Use OpenCode Go or another compatible provider."
	case strings.Contains(msg, "timeout"):
		return "Request timed out. The provider took too long to respond."
	case strings.Contains(msg, "port already in use") || strings.Contains(msg, "address already in use"):
		return "Cannot bind port: address already in use. Try EITRI_ADDR=127.0.0.1:8081 eitri."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "lookup"):
		return "Cannot reach provider at the configured URL. Check base_url in Settings."
	default:
		return "LLM error: " + stripHTMLTags(msg)
	}
}

// stripHTMLTags removes HTML tags from a string.
func stripHTMLTags(s string) string {
	// First strip content of <script>...</script> and <style>...</style> blocks.
	s = scriptTagContentRe.ReplaceAllString(s, "")
	s = styleTagContentRe.ReplaceAllString(s, "")

	// Then remove remaining HTML tags.
	result := htmlTagRe.ReplaceAllString(s, "")

	// Collapse multiple whitespace.
	result = strings.TrimSpace(whitespaceRe.ReplaceAllString(result, " "))

	// Truncate long messages to 200 chars.
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	return result
}

// MaxTurnsMessage returns a user-facing message for max-turn limits.
func MaxTurnsMessage(limit int) string {
	if limit == 1 {
		return "Stopped after reaching max turns limit (1). Increase Max Turns in Settings if this task needs tool follow-up steps."
	}
	return fmt.Sprintf("Stopped after reaching max turns limit (%d). Increase Max Turns in Settings if this task needs more tool/model steps.", limit)
}
