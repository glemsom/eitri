package engine

import (
	"os"
	"strings"
	"testing"
)

// TestSystemPromptEmbedded verifies the embed source-of-truth invariant: the
// compiled-in prompt is byte-identical to the checked-in markdown.
// This stops the embedded copy from drifting out of sync with the file.
func TestSystemPromptEmbedded(t *testing.T) {
	got, err := os.ReadFile("prompt.md")
	if err != nil {
		t.Fatalf("read prompt.md: %v", err)
	}
	if SystemPrompt != string(got) {
		t.Fatalf("embedded prompt != prompt.md\nembedded=%q\nfile=%q",
			SystemPrompt, string(got))
	}
}

// TestSystemPromptTokenBudget enforces the ceiling: the embedded
// prompt must stay under MaxSystemPromptTokens, since every head token is
// billed every turn as the byte-stable cache prefix.
func TestSystemPromptTokenBudget(t *testing.T) {
	if n := CountTokens(SystemPrompt); n > MaxSystemPromptTokens {
		t.Fatalf("system prompt %d tokens exceeds ceiling %d",
			n, MaxSystemPromptTokens)
	}
}

// TestSystemPromptNamesAgent locks the identity invariant:
// the system prompt must introduce the agent as Eitri, so the model answers
// to its own name in every session.
func TestSystemPromptNamesAgent(t *testing.T) {
	p := SystemPromptContent()
	if !strings.Contains(p, "You are Eitri") {
		t.Fatalf("system prompt does not introduce the agent as Eitri:\n%s", p)
	}
}

// TestSystemPromptIsStatic guards the byte-stable-head invariant:
// Eitri's system prompt must be constant text. Live session state (time, cwd)
// rides a tail message, never here.
func TestSystemPromptIsStatic(t *testing.T) {
	p := SystemPromptContent()
	if len(p) == 0 {
		t.Fatal("system prompt is empty")
	}
	if strings.ContainsAny(p, "$(") {
		t.Fatal("system prompt must not interpolate session state")
	}
}
