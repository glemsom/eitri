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

// TestSystemPromptYoloWithinTokenBudget guards the unsandboxed variant against
// the same silent drift: it must respect the system-prompt token ceiling.
func TestSystemPromptYoloWithinTokenBudget(t *testing.T) {
	t.Parallel()
	got := estimateString(SystemPromptYoloContent())
	if got > MaxSystemPromptTokens {
		t.Fatalf("yolo system prompt estimated at %d tokens, exceeds MaxSystemPromptTokens=%d; trim prompt_yolo.md", got, MaxSystemPromptTokens)
	}
}

// TestSystemPromptYoloMakesNoSandboxOrCageClaim guards the honest prompt
// variant: in an unsandboxed (--yolo-unsafe) session no cage runs, so the
// prompt must never promise a terminating sandbox to the agent.
func TestSystemPromptYoloMakesNoSandboxOrCageClaim(t *testing.T) {
	t.Parallel()
	yolo := SystemPromptYoloContent()
	for _, forbidden := range []string{"sandbox", "cage"} {
		if strings.Contains(strings.ToLower(yolo), forbidden) {
			t.Fatalf("yolo system prompt still claims a %q: %q", forbidden, yolo)
		}
	}
}

// TestSystemPromptVariantsCarrySubagentPointerOnly guards the subagents trim:
// both prompt variants must point at the `subagents` skill instead of
// embedding the batch recipe. The full guidance ships as the builtin skill
// (discoverable via the skill index), and the one-line pointer is the
// fallback trigger when the skill cannot be materialized.
func TestSystemPromptVariantsCarrySubagentPointerOnly(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"default": SystemPromptContent(),
		"yolo":    SystemPromptYoloContent(),
	} {
		if !strings.Contains(content, "see the `subagents` skill") {
			t.Errorf("%s prompt lost the subagents pointer", name)
		}
		for _, recipe := range []string{"mktemp -d", "agent_settled", "TMPDIR/subagent"} {
			if strings.Contains(content, recipe) {
				t.Errorf("%s prompt still embeds the subagents recipe (%q); the full guidance lives in the `subagents` skill", name, recipe)
			}
		}
	}
}
