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
	if n := estimateString(SystemPrompt); n > MaxSystemPromptTokens {
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
	for _, want := range []string{"ripgrep", "rg", "--heading", "--color=never", "files-with-matches"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must frame ripgrep usage as intent; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptDoesNotPrescribeRgBoilerplate(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, banned := range []string{"default to", "unless a different format is specifically needed"} {
		if strings.Contains(p, banned) {
			t.Fatalf("system prompt must not prescribe mandatory rg flag boilerplate; found %q:\n%s", banned, p)
		}
	}
}

func TestSystemPromptDoesNotInstructUnsettableReasoningEffort(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	if strings.Contains(p, "reasoning_effort") {
		t.Fatalf("system prompt must not instruct the agent to set reasoning_effort it cannot set:\n%s", p)
	}
}

func TestSystemPromptDocumentsBashFileOps(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	cases := map[string]string{
		"locate with rg":     "rg -n",
		"read numbered nl":   "nl -ba",
		"read range sed":     "sed -n",
		"edit in place":      "sed -i",
		"diff apply patch":   "git apply",
		"multi-line edit":    "multi-line",
		"write heredoc":      `cat <<'EOF'`,
		"verify re-read":     "re-read",
		"home skill pack":    "~/.agents/skills/",
		"project skill pack": ".agents/skills/",
	}
	for name, want := range cases {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must document %s via bash; missing %q:\n%s", name, want, p)
		}
	}
}

func TestSystemPromptGuidesToolSelection(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"web_fetch", "bash", "open_in_browser", "Markdown", "curl", "30s", "$TMPDIR", "host path", "not the first that springs to mind"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must guide tool selection; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptStatesOutputCapContract(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"capped", "+N more", "bytes truncated", "partial"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must state the output truncation contract; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptIsStatic(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	if len(p) == 0 {
		t.Fatal("system prompt is empty")
	}
	if strings.Contains(p, "$(") { // literal command substitution, e.g. $(cmd)(not any-char match)
		t.Fatal("system prompt must not interpolate session state")
	}
}
