// Package compactor provides a pure-function compactor that scans
// conversation history, finds tool-result messages that are old and large
// enough, and replaces each one with a concise LLM-generated summary.
//
// The package has no side effects — it takes messages in and returns
// messages out, making it fully unit-testable with a mock LLM service.
//
// # Usage
//
//	c := &compactor.Compactor{}
//	compacted, err := c.Compact(ctx, messages, llmSvc, thresholds)
//	if compacted != nil {
//	    // use compacted messages
//	}
//
// # Thresholds
//
// Compaction uses a two-threshold system:
//   - HighWater: when total estimated tokens exceed this value, compaction
//     triggers (e.g., 90% of the context window).
//   - LowWater: compaction stops once total estimated tokens fall below
//     this value (e.g., 30% of the context window).
package compactor
