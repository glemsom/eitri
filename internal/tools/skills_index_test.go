package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillSpec is a name/description pair for building deterministic test catalogs.
type skillSpec struct{ name, desc string }

// catalogFromSpecs builds a Catalog in-package with fixed Dirs under one root so
// the rendered bytes (and their token estimate) never depend on the machine.
func catalogFromSpecs(specs []skillSpec) *Catalog {
	c := &Catalog{skills: map[string]*Skill{}, order: nil}
	for _, s := range specs {
		c.skills[s.name] = &Skill{Dir: "/opt/eitri/skills/" + s.name, Description: s.desc, ModelInvocable: true}
		c.order = append(c.order, s.name)
	}
	return c
}

// fixedCatalog is the small fixed set the golden test pins.
func fixedCatalog() *Catalog {
	return catalogFromSpecs([]skillSpec{
		{"subagents", "Outsource part of your own workload to a subagent — a batch run with its own session and sandbox."},
		{"web-access", "Fetch and read web content — pages, API/JSON data, downloads — with curl and lynx recipes."},
	})
}

// TestRenderIndexGolden pins the exact rendered bytes of the compact catalog for
// a fixed skill set, so any format change is a deliberate edit to this string.
func TestRenderIndexGolden(t *testing.T) {
	c := fixedCatalog()
	want := skillIndexOpenForTest + "\n" +
		"[/opt/eitri/skills]\n" +
		"subagents: Outsource part of your own workload to a subagent — a batch run with its own session and sandbox.\n" +
		"web-access: Fetch and read web content — pages, API/JSON data, downloads — with curl and lynx recipes.\n" +
		skillIndexCloseForTest
	if got := c.RenderIndex(); got != want {
		t.Fatalf("rendered index bytes drifted from the pin:\n got: %q\nwant: %q", got, want)
	}
}

// TestRenderIndexDeclaresEachRootOnce guards the compaction: the absolute root
// prefix is emitted once in the header, never repeated per skill.
func TestRenderIndexDeclaresEachRootOnce(t *testing.T) {
	c := fixedCatalog()
	idx := c.RenderIndex()
	if n := strings.Count(idx, "[/opt/eitri/skills]"); n != 1 {
		t.Fatalf("root has %d headings, want exactly one:\n%s", n, idx)
	}
	if strings.Contains(idx, "<skill>") || strings.Contains(idx, "<path>") {
		t.Fatalf("per-skill XML scaffolding survived the compaction:\n%s", idx)
	}
}

// TestRenderIndexCapsDescriptionWidth guards the width bound: a verbose
// description is truncated to one capped line rather than bloating every turn.
func TestRenderIndexCapsDescriptionWidth(t *testing.T) {
	c := &Catalog{
		skills: map[string]*Skill{"verbose": {Dir: "/s/verbose", Description: strings.Repeat("word ", 100), ModelInvocable: true}},
		order:  []string{"verbose"},
	}
	var line string
	for _, l := range strings.Split(c.RenderIndex(), "\n") {
		if strings.HasPrefix(l, "verbose: ") {
			line = l
		}
	}
	desc := strings.TrimPrefix(line, "verbose: ")
	if !strings.HasSuffix(desc, "…") {
		t.Fatalf("over-wide description not truncated: %q", desc)
	}
	if got := len([]rune(desc)); got > maxDescriptionWidth {
		t.Fatalf("capped description is %d runes, want <= %d", got, maxDescriptionWidth)
	}
}

// TestRenderIndexFlattensMultiLineDescription guards the one-line-per-skill
// rule: embedded newlines collapse so a skill never spans several lines.
func TestRenderIndexFlattensMultiLineDescription(t *testing.T) {
	c := &Catalog{
		skills: map[string]*Skill{"multi": {Dir: "/s/multi", Description: "first\nsecond   third", ModelInvocable: true}},
		order:  []string{"multi"},
	}
	if got := c.RenderIndex(); !strings.Contains(got, "multi: first second third\n") {
		t.Fatalf("multi-line description not flattened to one line:\n%s", got)
	}
}

// skillIndexReferenceBaselineTokens is the reference catalogue's token estimate
// under the pre-compaction XML format (issue 01's measurement). The compact
// format must stay at least 30% under it.
const skillIndexReferenceBaselineTokens = 837

func TestRenderIndexTokenBudget(t *testing.T) {
	idx := referenceCatalog().RenderIndex()
	got := (len(idx) + 3) / 4
	if got > MaxSkillIndexTokens {
		t.Fatalf("reference index estimated at %d tokens, exceeds MaxSkillIndexTokens=%d; trim the format", got, MaxSkillIndexTokens)
	}
	if floor := skillIndexReferenceBaselineTokens * 7 / 10; got > floor {
		t.Fatalf("reference index estimated at %d tokens, not at least 30%% under the pre-compaction baseline %d (floor %d)", got, skillIndexReferenceBaselineTokens, floor)
	}
	t.Logf("reference index: %d chars, %d tokens (baseline %d)", len(idx), got, skillIndexReferenceBaselineTokens)
}

// TestRenderIndexLocatesSkillBodiesAndResources guards the model-visibility
// contract: from the catalog alone a reader can resolve every model-visible
// pack, open its SKILL.md, and find its bundled resources.
func TestRenderIndexLocatesSkillBodiesAndResources(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSkill(t, user, "alpha", "an alpha skill", "alpha body", map[string]string{"ref/notes.md": "notes"})
	writeSkill(t, proj, "beta", "a beta skill", "beta body", nil)
	catalog, err := Discover(user, proj, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	roots := parseRenderedIndex(t, catalog.RenderIndex())
	for _, name := range catalog.ModelVisibleSkills() {
		sk := catalog.Skill(name)
		root, ok := roots[name]
		if !ok {
			t.Fatalf("skill %q references an undeclared root", name)
		}
		dir := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Fatalf("catalog did not lead to %q's SKILL.md: %v", name, err)
		}
		for _, res := range sk.Resources {
			if _, err := os.Stat(filepath.Join(dir, res)); err != nil {
				t.Fatalf("catalog did not lead to %q's resource %q: %v", name, res, err)
			}
		}
	}
}

// parseRenderedIndex reads the bracketed root headings and the per-skill lines
// nested under them back out of the rendered block, returning each skill name's
// root, mirroring how a model resolves a skill's files.
func parseRenderedIndex(t *testing.T, idx string) map[string]string {
	t.Helper()
	root := ""
	byName := map[string]string{}
	for _, row := range strings.Split(idx, "\n") {
		if strings.HasPrefix(row, "[") && strings.HasSuffix(row, "]") {
			root = row[1 : len(row)-1]
			continue
		}
		if strings.HasPrefix(row, "# ") {
			continue
		}
		if name, _, ok := strings.Cut(row, ": "); ok {
			byName[name] = root
		}
	}
	return byName
}

func referenceCatalog() *Catalog {
	specs := []struct {
		name string
		desc string
	}{
		{"subagents", "Outsource part of your own workload to a subagent — a batch run with its own session and sandbox."},
		{"web-access", "Fetch and read web content — pages, API/JSON data, and downloads — with curl and lynx recipes."},
		{"code-review", "Review a branch, PR, or work-in-progress against the repo's documented standards and the originating spec."},
		{"clean-code", "Turn code that works into code that is clean, applying the principles from Clean Code."},
		{"tdd", "Build features and fix bugs test-first, red-green-refactor, with integration tests at pre-agreed seams."},
		{"diagnosing-bugs", "Diagnose hard bugs and performance regressions with a disciplined hypothesis loop."},
		{"domain-modeling", "Build and sharpen a project's domain model, GLOSSARY.md, and architecture decision records."},
		{"grilling", "Grill a plan, decision, or idea relentlessly to stress-test the thinking behind it."},
		{"research", "Investigate a question against high-trust primary sources and capture the findings as a Markdown file."},
		{"codebase-design", "Design deeper module interfaces, find deepening opportunities, and decide where the seams go."},
		{"resolving-merge-conflicts", "Resolve an in-progress git merge or rebase conflict safely and completely."},
		{"writing-for-agents", "Write documents for agents: skills, AGENTS.md, and instruction files that stay crisp."},
		{"commit", "Craft a tidy, well-scoped commit message that explains the change and why it was made."},
		{"refactor", "Refactor a module without changing behaviour, keeping the diff small and the intent clear."},
		{"perf", "Profile a hot path and cut its cost, measuring before and after so the win is real."},
		{"docs", "Write and refresh user-facing documentation so it matches the code it describes."},
	}
	out := make([]skillSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, skillSpec{name: s.name, desc: s.desc})
	}
	return catalogFromSpecs(out)
}

// skillIndexOpenForTest / skillIndexCloseForTest pin the exact markers the
// golden test asserts, independent of the shared constant.
const (
	skillIndexOpenForTest  = "<available_skills>"
	skillIndexCloseForTest = "</available_skills>"
)

// TestRenderIndexShortDescriptionTier guards the tier split: a declared short
// description is what stays resident, and a skill without one falls back to its
// full description.
func TestRenderIndexShortDescriptionTier(t *testing.T) {
	user := t.TempDir()
	writeSkillMeta(t, user, "shortened", "the long full description that should stay off the wire", "body", nil,
		[]string{"short-description: a tight one-liner"})
	writeSkill(t, user, "fallback", "the full description used when no short one is declared", "body", nil)
	catalog, err := Discover(user, t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	idx := catalog.RenderIndex()
	if !strings.Contains(idx, "shortened: a tight one-liner\n") {
		t.Fatalf("catalog did not show the declared short description:\n%s", idx)
	}
	if strings.Contains(idx, "should stay off the wire") {
		t.Fatalf("catalog leaked the full description despite a short one:\n%s", idx)
	}
	if !strings.Contains(idx, "fallback: the full description used when no short one is declared\n") {
		t.Fatalf("catalog did not fall back to the full description:\n%s", idx)
	}
}
