package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/skills"
)

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\n# " + name
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestBuildSystemPromptIncludesSkillsCatalog(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "code-review"), "code-review")
	svc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})

	prompt := BuildSystemPrompt("custom prompt", svc)
	for _, want := range []string{"custom prompt", "<available_skills>", "<name>code-review</name>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}
