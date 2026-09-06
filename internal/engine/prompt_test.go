package engine

import (
	"strings"
	"testing"
)

// TestSystemPromptWithinTokenBudget guards against silent drift: prompt.md is
// edited freely, but its estimated size must stay under the declared cap.
func TestSystemPromptWithinTokenBudget(t *testing.T) {
	t.Parallel()
	got := estimateString(SystemPromptContent())
	if got > MaxSystemPromptTokens {
		t.Fatalf("system prompt estimated at %d tokens, exceeds MaxSystemPromptTokens=%d; trim prompt.md", got, MaxSystemPromptTokens)
	}
}

// TestSystemPromptCarriesSubagentPointerOnly guards the subagents trim: the
// prompt must point at the `subagents` skill instead of embedding the batch
// recipe. The full guidance ships as the builtin skill (discoverable via the
// skill index), and the one-line pointer is the fallback trigger when the
// skill cannot be materialized.
func TestSystemPromptCarriesSubagentPointerOnly(t *testing.T) {
	t.Parallel()
	content := SystemPromptContent()
	if !strings.Contains(content, "see the `subagents` skill") {
		t.Error("prompt lost the subagents pointer")
	}
	for _, recipe := range []string{"mktemp -d", "agent_settled", "TMPDIR/subagent"} {
		if strings.Contains(content, recipe) {
			t.Errorf("prompt still embeds the subagents recipe (%q); the full guidance lives in the `subagents` skill", recipe)
		}
	}
}
