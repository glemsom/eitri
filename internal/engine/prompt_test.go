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

// TestSystemPromptYoloIsADistinctVariant anchors the yolo prompt as its own
// byte-stable head: it differs from the default, and the default still carries
// the sandbox sentence the yolo variant honestly drops — so the no-claim test
// cannot silently pass on an unchanged copy.
func TestSystemPromptYoloIsADistinctVariant(t *testing.T) {
	t.Parallel()
	if SystemPromptYoloContent() == SystemPromptContent() {
		t.Fatal("yolo variant must differ from the default prompt")
	}
	if !strings.Contains(SystemPromptContent(), "sandbox") {
		t.Fatal("default prompt lost its sandbox guidance; the yolo no-claim test is no longer meaningful")
	}
}
