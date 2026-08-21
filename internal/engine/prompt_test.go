package engine

import (
	"os"
	"strings"
	"testing"
)

func TestSystemPromptEmbedded(t *testing.T) {
	t.Parallel()
	got, err := os.ReadFile("prompt.md")
	if err != nil {
		t.Fatalf("read prompt.md: %v", err)
	}
	if SystemPrompt != string(got) {
		t.Fatalf("embedded prompt != prompt.md\nembedded=%q\nfile=%q",
			SystemPrompt, string(got))
	}
}

func TestSystemPromptTokenBudget(t *testing.T) {
	t.Parallel()
	if n := CountTokens(SystemPrompt); n > MaxSystemPromptTokens {
		t.Fatalf("system prompt %d tokens exceeds ceiling %d",
			n, MaxSystemPromptTokens)
	}
}

func TestSystemPromptNamesAgent(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	if !strings.Contains(p, "You are Eitri") {
		t.Fatalf("system prompt does not introduce the agent as Eitri:\n%s", p)
	}
}

func TestSystemPromptPrefersRipgrep(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"ripgrep", "rg", "grep", "over grep", "--heading", "--color=never"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must instruct preferred ripgrep usage; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptIsStatic(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	if len(p) == 0 {
		t.Fatal("system prompt is empty")
	}
	if strings.ContainsAny(p, "$(") {
		t.Fatal("system prompt must not interpolate session state")
	}
}
