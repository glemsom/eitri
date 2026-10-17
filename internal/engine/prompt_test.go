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

// TestSystemPromptDefersSkillDiscoveryToTheIndex guards the trim: both the
// skill-loading mechanics (the index shape, reading SKILL.md) and per-skill
// reach-for pointers duplicate what the skill index already carries — each
// entry ships its own "when to use" description and is re-injected each turn.
// The prompt keeps only the behavioural nudge to consult that index.
func TestSystemPromptDefersSkillDiscoveryToTheIndex(t *testing.T) {
	t.Parallel()
	content := SystemPromptContent()
	for _, restated := range []string{
		"SKILL.md",
		"bracketed heading",
		"skill index is re-sent",
		"`web-access` skill",
		"`subagents` skill",
	} {
		if strings.Contains(content, restated) {
			t.Errorf("prompt restates %q, which the skill index already carries; drop it", restated)
		}
	}
	if !strings.Contains(content, "Consult the index") {
		t.Error("prompt lost the nudge to consult the skill index")
	}
}

// TestSystemPromptOmitsSubagentRecipe guards that the prompt defers subagent
// discovery to the skill index rather than embedding the batch recipe, which
// ships as the builtin `subagents` skill.
func TestSystemPromptOmitsSubagentRecipe(t *testing.T) {
	t.Parallel()
	content := SystemPromptContent()
	for _, recipe := range []string{"mktemp -d", "agent_settled", "TMPDIR/subagent"} {
		if strings.Contains(content, recipe) {
			t.Errorf("prompt embeds the subagents recipe (%q); the full guidance lives in the `subagents` skill", recipe)
		}
	}
}
