package engine

import "testing"

// TestSystemPromptWithinTokenBudget guards against silent drift: prompt.md is
// edited freely, but its estimated size must stay under the declared cap.
func TestSystemPromptWithinTokenBudget(t *testing.T) {
	t.Parallel()
	got := estimateString(SystemPromptContent())
	if got > MaxSystemPromptTokens {
		t.Fatalf("system prompt estimated at %d tokens, exceeds MaxSystemPromptTokens=%d; trim prompt.md", got, MaxSystemPromptTokens)
	}
}
