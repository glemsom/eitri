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
		"locate with rg":        "rg -n",
		"read numbered nl":      "nl -ba",
		"read range sed":        "sed -n",
		"emit a diff":           "emit a diff",
		"diff apply patch":      "patch",
		"single editing method": "single editing method",
		"write heredoc":         `cat <<'EOF'`,
		"home skill pack":       "~/.agents/skills/",
		"project skill pack":    ".agents/skills/",
	}
	for name, want := range cases {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must document %s via bash; missing %q:\n%s", name, want, p)
		}
	}
}

func TestSystemPromptGuidesBashChains(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"&&", "set -euo pipefail", "STEP:", "later success can hide"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must guide bash-chain safety; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptGuidesToolSelection(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"curl", "bash", "open_in_browser", "--fail", "--max-time", "$TMPDIR", "host path", "not the first that springs to mind"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must guide tool selection; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptGuidesHTMLLynxRendering(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	for _, want := range []string{"curl --fail --max-time 30", "| lynx -dump -nolist -stdin", "JSON/data", "say so", "don't fabricate"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must carry lean HTML-rendering guidance; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptDoesNotStateOutputCapContract(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	// The output-cap + compression contract (line/byte capping, the "+N more"
	// and "+N bytes truncated" markers) lives exactly once, in the bash tool
	// description (single authoritative prompt, one fixed text).
	for _, banned := range []string{"capped", "+N more", "bytes truncated"} {
		if strings.Contains(p, banned) {
			t.Fatalf("system prompt must not restate the output-cap contract; found %q:\n%s", banned, p)
		}
	}
}

func TestSystemPromptStatesDeclaredToolset(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	// The prompt promises the declared toolset unconditionally (enforced at
	// boot); the base substrate is assumed present.
	for _, want := range []string{"declared toolset", "guaranteed", "coreutils", "rg", "curl", "lynx", "patch", "python3"} {
		if !strings.Contains(p, want) {
			t.Fatalf("system prompt must promise the declared toolset; missing %q:\n%s", want, p)
		}
	}
}

func TestSystemPromptStaysLeanOnToolPresence(t *testing.T) {
	t.Parallel()
	p := SystemPromptContent()
	// Tools outside the guaranteed toolset must neither be promised nor
	// hedged about. Editing is a single, patch-based method.
	for _, banned := range []string{"usually present", "never guaranteed", "git diff", "git apply", "sed -i"} {
		if strings.Contains(p, banned) {
			t.Fatalf("system prompt must not hedge non-guaranteed tools or multi-method edits; found %q:\n%s", banned, p)
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
