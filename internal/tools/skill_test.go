package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill writes a skill pack dir under root: <root>/<name>/SKILL.md with the
// given frontmatter name/description and body. Bundled files can be added via
// files (a map of relative path -> content).
func writeSkill(t *testing.T, root, name, description, body string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	for rel, fc := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(fc), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// TestSkillDiscoverScopes discovers skills from both user-global and project
// roots and verifies the union comes back with per-skill name/description.
func TestSkillDiscoverScopes(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSkill(t, user, "user-skill", "a user-global skill", "user body", nil)
	writeSkill(t, proj, "proj-skill", "a project skill", "proj body", nil)

	cb := &warningSink{}
	catalog, err := Discover(user, proj, cb)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	got := catalog.Names()
	if !contains(got, "user-skill") || !contains(got, "proj-skill") {
		t.Fatalf("discovered names = %v, want both user-skill and proj-skill", got)
	}
	us := catalog.Skill("user-skill")
	if us == nil || !strings.Contains(us.Body, "user body") {
		t.Fatalf("user-skill missing or body wrong: %+v", us)
	}
	ps := catalog.Skill("proj-skill")
	if ps == nil || !strings.Contains(ps.Body, "proj body") {
		t.Fatalf("proj-skill missing or body wrong: %+v", ps)
	}
	if cb.count != 0 {
		t.Fatalf("unexpected warnings = %d (%v), want 0", cb.count, cb.warns)
	}

	// The TUI's skills panel reads install scope + activation state via Items.
	if catalog.Scope("user-skill") != "user" || catalog.Scope("proj-skill") != "project" {
		t.Fatalf("install scopes unexpected: user-skill=%q proj-skill=%q", catalog.Scope("user-skill"), catalog.Scope("proj-skill"))
	}
	active := 0
	for _, it := range catalog.Items() {
		if it.Active {
			active++
		}
	}
	if active != 0 {
		t.Fatalf("fresh catalog has %d active skills, want 0", active)
	}
	// Marking a skill active is reflected in Items for the panel.
	catalog.MarkActive("user-skill")
	for _, it := range catalog.Items() {
		if it.Name == "user-skill" && !it.Active {
			t.Fatalf("user-skill should be active after MarkActive: %+v", it)
		}
	}
}

// TestSkillProjectShadowsUser verifies the exact-name collision rule: a project
// skill shadows the user-global skill of the same name.
func TestSkillProjectShadowsUser(t *testing.T) {
	user := t.TempDir()
	proj := t.TempDir()
	writeSkill(t, user, "dupe", "user version", "USER BODY", nil)
	writeSkill(t, proj, "dupe", "project version", "PROJECT BODY", nil)

	catalog, err := Discover(user, proj, &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if len(catalog.Names()) != 1 {
		t.Fatalf("names = %v, want exactly 1 (project shadows user)", catalog.Names())
	}
	s := catalog.Skill("dupe")
	if s == nil || !strings.Contains(s.Body, "PROJECT BODY") {
		t.Fatalf("shadowing failed: %+v", s)
	}
}

// TestSkillUnparseableOmittedFailClosed discovers a skill whose SKILL.md lacks
// a parseable name/description frontmatter and verifies it is omitted (with a
// warning) rather than surfaced to the model.
func TestSkillUnparseableOmittedFailClosed(t *testing.T) {
	user := t.TempDir()
	bad := filepath.Join(user, "broken")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No frontmatter block, no name/description.
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("# No frontmatter here\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cb := &warningSink{}
	catalog, err := Discover(user, t.TempDir(), cb)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if len(catalog.Names()) != 0 {
		t.Fatalf("names = %v, want 0 (unparseable omitted)", catalog.Names())
	}
	if cb.count == 0 {
		t.Fatal("expected a warning for the unparseable skill, got none")
	}
}

// TestSkillStripsFrontmatterOnActivation verifies the SKILL.md frontmatter is
// stripped from the returned body and bundled resources are advertised.
func TestSkillStripsFrontmatterOnActivation(t *testing.T) {
	user := t.TempDir()
	writeSkill(t, user, "res", "has resources",
		"## Instructions\n\nuse the script below",
		map[string]string{"scripts/run.sh": "#!/bin/sh\n", "references/api.md": "ref content\n"})

	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	if !contains(rd.Names(), "skill") {
		t.Fatalf("skill tool not registered when skills exist: names = %v", rd.Names())
	}
	out, err := rd.Run(context.Background(), "skill", argMap("name", "res"))
	if err != nil {
		t.Fatalf("skill Run error = %v, want nil", err)
	}
	if strings.Contains(out, "name:") && strings.Contains(out, "description:") {
		t.Fatalf("frontmatter not stripped from payload: %s", out)
	}
	if !strings.Contains(out, "## Instructions") {
		t.Fatalf("activated body missing instructions: %s", out)
	}
	if !strings.Contains(out, "scripts/run.sh") || !strings.Contains(out, "references/api.md") {
		t.Fatalf("bundled resources not advertised: %s", out)
	}
}

// TestSkillZeroOmitsTool verifies the skill tool is entirely omitted when no
// skills are discovered.
func TestSkillZeroOmitsTool(t *testing.T) {
	catalog, err := Discover(t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	for _, n := range rd.Names() {
		if n == "skill" {
			t.Fatalf("skill tool registered despite zero skills: %v", rd.Names())
		}
	}
}

// TestSkillDedupesReactivation verifies re-activating an already-in-context
// skill skips re-injection of the body (returns a short dedupe notice).
func TestSkillDedupesReactivation(t *testing.T) {
	user := t.TempDir()
	writeSkill(t, user, "s1", "first", "long body A\n", nil)

	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	first, err := rd.Run(context.Background(), "skill", argMap("name", "s1"))
	if err != nil {
		t.Fatalf("first Run error = %v, want nil", err)
	}
	if !strings.Contains(first, "long body A") {
		t.Fatalf("first activation missing body: %s", first)
	}
	second, err := rd.Run(context.Background(), "skill", argMap("name", "s1"))
	if err != nil {
		t.Fatalf("second Run error = %v, want nil", err)
	}
	if strings.Contains(second, "long body A") {
		t.Fatalf("re-activation re-injected body: %q", second)
	}
	if !strings.Contains(second, "already active") {
		t.Fatalf("re-activation notice missing: %q", second)
	}
}

// TestSkillSchemaEnumConstrained verifies the skill tool schema binds `name` to
// an enum of exactly the discovered, filtered skill names with the strict shape
// (additionalProperties:false + all-required).
func TestSkillSchemaEnumConstrained(t *testing.T) {
	user := t.TempDir()
	writeSkill(t, user, "alpha", "a", "body a", nil)
	writeSkill(t, user, "beta", "b", "body b", nil)
	// An unparseable skill must not leak into the enum.
	bad := filepath.Join(user, "nope")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("no fm\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	var skillDef *Definition
	for _, d := range rd.Definitions() {
		if d.Name == "skill" {
			skillDef = &d
		}
	}
	if skillDef == nil {
		t.Fatal("skill definition missing")
	}
	props, _ := skillDef.Parameters["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("schema properties missing: %v", skillDef.Parameters)
	}
	nameProp, _ := props["name"].(map[string]any)
	if nameProp == nil {
		t.Fatalf("name property missing: %v", props)
	}
	enum, _ := nameProp["enum"].([]any)
	if len(enum) != 2 {
		t.Fatalf("enum = %v, want exactly [alpha beta], excluding unparseable 'nope'", enum)
	}
	for _, e := range enum {
		if e == "nope" {
			t.Fatalf("unparseable skill leaked into enum: %v", enum)
		}
	}
	if rd.Definitions()[0].Parameters["additionalProperties"] != false {
		t.Fatalf("schema missing additionalProperties:false: %v", skillDef.Parameters)
	}
}

// TestSkillDisableModelInvocationHidden verifies hide-not-block: a valid pack
// declaring disable-model-invocation: true is omitted from the catalog and the
// tool enum (filtered, not listed to be blocked at call time).
func TestSkillDisableModelInvocationHidden(t *testing.T) {
	user := t.TempDir()
	writeSkill(t, user, "normal", "invocable", "body n", nil)
	// A pack with disable-model-invocation: true in its frontmatter.
	dir := filepath.Join(user, "manual")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: manual\ndescription: only user invokes\ndisable-model-invocation: true\n---\n\nmanual body\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	cb := &warningSink{}
	catalog, err := Discover(user, t.TempDir(), cb)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	names := catalog.Names()
	if len(names) != 1 || names[0] != "normal" {
		t.Fatalf("names = %v, want exactly [normal] (manual hidden by disable-model-invocation)", names)
	}
	// Hiding is not an error, so it must not warn.
	if cb.count != 0 {
		t.Fatalf("unexpected warnings = %d, want 0 (hidden != unparseable)", cb.count)
	}
}

// testDeps builds registry deps for a skill test with an injected catalog and a
// throwaway workspace (no bash runner needed).
func testDeps(t *testing.T, workspace string, catalog *Catalog) Deps {
	t.Helper()
	if workspace == "" {
		workspace = t.TempDir()
	}
	return Deps{
		Workspace: workspace,
		TempHost:  filepath.Join(t.TempDir(), "eitri-g"),
		GUID:      GUID("tguid"),
		Skills:    catalog,
	}
}

// warningSink captures discovery warnings.
type warningSink struct {
	warns []string
	count int
}

func (w *warningSink) Warnf(format string, args ...any) {
	w.warns = append(w.warns, format)
	w.count++
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
