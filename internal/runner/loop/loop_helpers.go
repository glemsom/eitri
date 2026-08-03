package loop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

// normalizeToolCallIDs rewrites provider-emitted tool IDs that are unsafe to
// replay in later requests. Some OpenAI Responses-compatible gateways emit
// opaque output item IDs (long base64 strings with '/' and '+') when no call_id
// is present; replaying those as function_call.call_id is rejected on the next
// turn. The assistant tool call and matching tool result only need a stable
// conversation-local ID, so replace unsafe IDs with deterministic call_* IDs.
func normalizeToolCallIDs(toolCalls []litellm.ToolUseBlock) []litellm.ToolUseBlock {
	if len(toolCalls) == 0 {
		return toolCalls
	}
	out := make([]litellm.ToolUseBlock, len(toolCalls))
	copy(out, toolCalls)
	for i := range out {
		if isSafeReplayToolID(out[i].ID) {
			continue
		}
		seed := fmt.Sprintf("%d\x00%s\x00%s\x00%s", i, out[i].ID, out[i].Name, string(out[i].Arguments))
		sum := sha256.Sum256([]byte(seed))
		out[i].ID = "call_eitri_" + hex.EncodeToString(sum[:8])
	}
	return out
}

// normalizeToolCallArguments repairs streamed tool arguments that arrive as
// concatenated JSON values instead of a single JSON object.
func normalizeToolCallArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if json.Valid(raw) {
		return append(json.RawMessage(nil), raw...)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var last any
	seen := false
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			break
		}
		last = v
		seen = true
	}
	if !seen {
		return append(json.RawMessage(nil), raw...)
	}

	normalized, err := json.Marshal(last)
	if err != nil || !json.Valid(normalized) {
		return append(json.RawMessage(nil), raw...)
	}
	return normalized
}

// normalizeToolCalls prepares provider-emitted tool calls for replay.
// It fixes unsafe IDs, repairs malformed streamed arguments, and collapses
// duplicate IDs so litellm request validation accepts the assistant message.
func normalizeToolCalls(toolCalls []litellm.ToolUseBlock) []litellm.ToolUseBlock {
	if len(toolCalls) == 0 {
		return toolCalls
	}

	collapsed := make([]litellm.ToolUseBlock, 0, len(toolCalls))
	rawSeen := make(map[string]int, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.ID != "" {
			if idx, ok := rawSeen[tc.ID]; ok {
				collapsed[idx] = tc
				continue
			}
			rawSeen[tc.ID] = len(collapsed)
		}
		collapsed = append(collapsed, tc)
	}

	out := make([]litellm.ToolUseBlock, 0, len(collapsed))
	seen := make(map[string]int, len(collapsed))
	for _, tc := range normalizeToolCallIDs(collapsed) {
		tc.Arguments = normalizeToolCallArguments(tc.Arguments)
		if idx, ok := seen[tc.ID]; ok {
			out[idx] = tc
			continue
		}
		seen[tc.ID] = len(out)
		out = append(out, tc)
	}
	return out
}

func isSafeReplayToolID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

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
