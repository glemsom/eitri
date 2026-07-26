// Package compactor provides a pure-function compactor that scans
// conversation history, finds messages that are large enough,
// and replaces each one with a concise LLM-generated summary.
//
// The package has no side effects — it takes messages in and returns
// messages out, making it fully unit-testable with a mock LLM service.
//
// # Usage
//
//	c := &compactor.Compactor{}
//	compacted, count, freed, pruned, err := c.Compact(ctx, messages, llmSvc, thresholds)
//	if compacted != nil {
//	    // use compacted messages
//	    // count = number of messages compacted
//	    // freed = approximate tokens freed
//	    // pruned = number of tool-call argument blocks pruned
//	}
//
// # Thresholds
//
// Compaction uses a two-threshold system:
//   - HighWater: when total estimated tokens exceed this value, compaction
//     triggers (e.g., 90% of the context window).
//   - LowWater: compaction stops once total estimated tokens fall below
//     this value (e.g., 30% of the context window).
//   - MessageSizeThreshold: minimum estimated-token count for an individual
//     message to be eligible for compaction. Messages below this threshold
//     are skipped. A value of 0 means no threshold (all messages eligible).
//   - ToolCallRetentionTurns: number of recent assistant messages whose ToolCall
//     arguments are preserved. Older assistant messages have their arguments
//     pruned to a compact placeholder. 0 means no pruning.
//
// # Role-aware compaction
//
// The compactor handles all message roles (user, assistant, tool):
//   - Tool messages are compacted with a "[TOOL RESULT COMPACTED]" prefix.
//   - User and assistant messages are compacted with a "[MESSAGE COMPACTED]" prefix.
//   - Assistant messages retain their ToolCalls after compaction — only the
//     text Content field is replaced.
//   - System messages are never compacted.
//   - Role-appropriate summarization prompts are used for each role.
//
// Already-compacted messages (detected by either prefix) are skipped to
// avoid re-compaction.
//
// # Tool call argument pruning
//
// When ToolCallRetentionTurns > 0, the compactor scans assistant messages and
// prunes the Function.Arguments field of ToolCalls on messages beyond the
// retention window. The ID and Function.Name are preserved. Arguments are
// replaced with a placeholder like {"pruned": "~N chars"} encoding the original
// size. Already-pruned tool calls (detectable via the placeholder prefix) are
// not re-pruned.
package compactor
