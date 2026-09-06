package skillspack_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/engine/skillspack"
	"github.com/glemsom/eitri/internal/tools"
)

type warningSink struct {
	warns []string
	count int
}

func (w *warningSink) Warnf(format string, args ...any) {
	w.warns = append(w.warns, fmt.Sprintf(format, args...))
	w.count++
}

// writeEmbeddedPack materializes the embedded pack into root so the normal
// discovery loader can walk it like any other skill root.
func writeEmbeddedPack(t *testing.T, root string) {
	t.Helper()
	packDir := filepath.Join(root, "subagents")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", packDir, err)
	}
	data, err := skillspack.FS.ReadFile(filepath.Join("subagents", "SKILL.md"))
	if err != nil {
		t.Fatalf("read embedded subagents/SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "SKILL.md"), data, 0o600); err != nil {
		t.Fatalf("write subagents/SKILL.md: %v", err)
	}
}

func TestEmbeddedSubagentsPackDiscoversAsModelInvocableSkill(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeEmbeddedPack(t, root)

	quiet := &warningSink{}
	catalog, err := tools.Discover(root, t.TempDir(), t.TempDir(), quiet)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if got := catalog.Names(); len(got) != 1 || got[0] != "subagents" {
		t.Fatalf("discovered names = %v, want [subagents]", got)
	}
	sk := catalog.Skill("subagents")
	if sk == nil {
		t.Fatal("subagents skill not in catalog")
	}
	if !sk.ModelInvocable {
		t.Fatal("embedded subagents pack must be model-invocable")
	}
	if strings.TrimSpace(sk.Description) == "" {
		t.Fatal("subagents description must be non-empty")
	}
	if _, err := os.Stat(filepath.Join(sk.Dir, "SKILL.md")); err != nil {
		t.Fatalf("rendered SKILL.md path not readable: %v", err)
	}
	if quiet.count != 0 {
		t.Fatalf("unexpected discovery warnings = %d (%v), want 0", quiet.count, quiet.warns)
	}
}

func TestEmbeddedSubagentsPackBodyContract(t *testing.T) {
	t.Parallel()
	data, err := skillspack.FS.ReadFile(filepath.Join("subagents", "SKILL.md"))
	if err != nil {
		t.Fatalf("read embedded subagents/SKILL.md: %v", err)
	}
	body := string(data)

	// The recipe scaffolding ports verbatim from the prompt sections.
	for _, want := range []string{
		`agent_dir=$(mktemp -d "$TMPDIR/subagent.XXXXXX")`,
		`EITRI_DIR="$agent_dir"`,
		`EITRI_CONFIG="${EITRI_CONFIG:-$HOME/.eitri/config.json}"`,
		`wait "${pids[`,
		"agent_settled",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing verbatim recipe fragment %q", want)
		}
	}

	// The one clause the default and yolo prompt sections differ on becomes a
	// mode-agnostic invariant: wait and read within the same Bash call covers
	// both a terminating sandbox and nothing terminating at all.
	for _, want := range []string{
		"same Bash tool call",
		"terminates child processes when the tool call returns",
		"merely keep running",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing mode-agnostic invariant fragment %q", want)
		}
	}
}
