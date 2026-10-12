package skillspack_test

import (
	"fmt"
	"os"
	"os/exec"
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
		`parent_config="${EITRI_CONFIG:-${EITRI_DIR:-$HOME/.eitri}/config.json}"`,
		`EITRI_DIR="$agent_dir" EITRI_CONFIG="$parent_config"`,
		`wait "${pids[`,
		"--format json",
		"jq -r .answer",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing verbatim recipe fragment %q", want)
		}
	}

	// Subagent results are read from the --format json envelope's answer field,
	// never parsed out of prose stdout (no agent_settled marker hunting).
	if strings.Contains(body, "agent_settled") {
		t.Fatal("body still parses prose stdout for settled markers; read the answer field from the --format json envelope instead")
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

// subagentsRecipe returns the first ```sh fenced block of the pack body.
func subagentsRecipe(t *testing.T, body string) string {
	t.Helper()
	const open = "```sh\n"
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatal("no ```sh block in subagents body")
	}
	rest := body[start+len(open):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatal("unterminated ```sh block in subagents body")
	}
	return rest[:end]
}

// TestSubagentsRecipePropagatesParentConfig runs the launch recipe with a stub
// eitri and pins the one thing the shell gets subtly wrong: repointing EITRI_DIR
// in the same command that resolves the config fallback would leave every
// subagent on its own fresh data directory instead of the parent's config.
func TestSubagentsRecipePropagatesParentConfig(t *testing.T) {
	t.Parallel()
	data, err := skillspack.FS.ReadFile(filepath.Join("subagents", "SKILL.md"))
	if err != nil {
		t.Fatalf("read embedded subagents/SKILL.md: %v", err)
	}
	recipe := subagentsRecipe(t, string(data))

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	parentDir := filepath.Join(dir, "parent")
	for _, d := range []string{binDir, parentDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	seen := filepath.Join(dir, "seen.txt")
	stub := "#!/bin/sh\n" +
		`printf '%s\n' "$EITRI_CONFIG" >> "$SEEN"` + "\n" +
		`printf '%s\n' '{"answer":"ok","session":"s","turns":1,"stopped":false}'` + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "eitri"), []byte(stub), 0o700); err != nil {
		t.Fatalf("write stub eitri: %v", err)
	}

	cmd := exec.Command("bash", "-c", recipe)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"SEEN="+seen,
		"TMPDIR="+dir,
		"EITRI_DIR="+parentDir,
		"EITRI_CONFIG=",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recipe failed (%v):\n%s", err, out)
	}

	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("stub eitri never ran: %v", err)
	}
	want := filepath.Join(parentDir, "config.json")
	if lines := strings.Fields(string(got)); len(lines) != 2 {
		t.Fatalf("expected 2 subagent launches, got %q", lines)
	}
	for _, line := range strings.Fields(string(got)) {
		if line != want {
			t.Fatalf("subagent booted with EITRI_CONFIG=%q, want the parent's %q", line, want)
		}
	}
}
