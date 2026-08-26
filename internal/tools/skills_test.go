package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, description, body string, files map[string]string) {
	t.Helper()
	writeSkillMeta(t, root, name, description, body, files, nil)
}

// writeSkillMeta writes a skill pack with optional extra frontmatter lines appended
// after description (e.g. "model-invocable: false").
func writeSkillMeta(t *testing.T, root, name, description, body string, files map[string]string, extraFront []string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir %s: %v", dir, err)
	}
	meta := "name: " + name + "\ndescription: " + description
	for _, line := range extraFront {
		meta += "\n" + line
	}
	content := "---\n" + meta + "\n---\n\n" + body
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

func TestSkillDiscoverScopes(t *testing.T) {
	t.Parallel()
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

	if catalog.Scope("user-skill") != "user" || catalog.Scope("proj-skill") != "project" {
		t.Fatalf("install scopes unexpected: user-skill=%q proj-skill=%q", catalog.Scope("user-skill"), catalog.Scope("proj-skill"))
	}
}

func TestSkillProjectShadowsUser(t *testing.T) {
	t.Parallel()
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

func TestSkillUnparseableOmittedFailClosed(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	bad := filepath.Join(user, "broken")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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

func TestActivateSkillRendersStrippedPayload(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkill(t, user, "res", "has resources",
		"## Instructions\n\nuse the script below",
		map[string]string{"scripts/run.sh": "#!/bin/sh\n", "references/api.md": "ref content\n"})

	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	res, err := rd.ActivateSkill(context.Background(), "res")
	if err != nil {
		t.Fatalf("ActivateSkill error = %v, want nil", err)
	}
	out := res.Text
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

func TestActivateSkillNoSkills(t *testing.T) {
	t.Parallel()
	rd := NewRegistry(testDeps(t, "", nil))
	if _, err := rd.ActivateSkill(context.Background(), "any"); err == nil {
		t.Fatal("ActivateSkill with nil catalog = nil error, want an error")
	}
}

func TestActivateSkillUnknownName(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkill(t, user, "s1", "first", "body", nil)
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	if _, err := rd.ActivateSkill(context.Background(), "nope"); err == nil {
		t.Fatal("ActivateSkill unknown name = nil error, want an error")
	}
}

func TestActivateSkillReappliesEveryTime(t *testing.T) {
	t.Parallel()
	// The slash surface is an explicit human re-invoke: it must always re-apply
	// the full payload, never dedupe against a prior activation.
	user := t.TempDir()
	writeSkill(t, user, "s1", "first", "long body A\n", nil)
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	for i := 0; i < 2; i++ {
		res, err := rd.ActivateSkill(context.Background(), "s1")
		if err != nil {
			t.Fatalf("ActivateSkill #%d error = %v, want nil", i, err)
		}
		if !strings.Contains(res.Text, "long body A") {
			t.Fatalf("ActivateSkill #%d missing body: %q", i, res.Text)
		}
	}
}

func TestSkillCatalogDoesNotExposeModelTool(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkill(t, user, "s1", "first", "body", nil)
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd := NewRegistry(testDeps(t, "", catalog))
	for _, n := range rd.Names() {
		if n == "skill" {
			t.Fatalf("skill tool still registered; names = %v", rd.Names())
		}
	}
	for _, d := range rd.Definitions() {
		if d.Name == "skill" {
			t.Fatalf("skill definition still exposed to the provider: %v", d)
		}
	}
}

func TestModelInvocableDefaultTrue(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkill(t, user, "default-on", "visible by default", "body", nil)
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	s := catalog.Skill("default-on")
	if s == nil || !s.ModelInvocable {
		t.Fatalf("ModelInvocable default = %v, want true", s)
	}
}

func TestModelInvocableParsesSynonyms(t *testing.T) {
	t.Parallel()
	for i, tc := range []struct {
		front string
		want  bool
	}{
		{"model-invocable: false", false},
		{"disable-model-invocation: true", false},
		{"disable-model-invocation: false", true},
		{"model-invocable: true", true},
	} {
		user := t.TempDir()
		name := fmt.Sprintf("s%d", i)
		writeSkillMeta(t, user, name, "desc", "body", nil, []string{tc.front})
		catalog, err := Discover(user, t.TempDir(), &warningSink{})
		if err != nil {
			t.Fatalf("Discover error = %v, want nil", err)
		}
		if got := catalog.Skill(name).ModelInvocable; got != tc.want {
			t.Fatalf("%s: ModelInvocable = %v, want %v", tc.front, got, tc.want)
		}
	}
}

func TestCatalogModelVisibleSkillsSortedAndFiltered(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkillMeta(t, user, "b-skill", "b desc", "b", nil, []string{"model-invocable: false"})
	writeSkill(t, user, "a-skill", "a desc", "a", nil)
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	got := catalog.ModelVisibleSkills()
	if len(got) != 1 || got[0] != "a-skill" {
		t.Fatalf("ModelVisibleSkills = %v, want [a-skill]", got)
	}
}

func TestRenderIndexBlock(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	proj := t.TempDir()
	// hidden skill omitted
	writeSkillMeta(t, user, "hidden", "h", "body", nil, []string{"model-invocable: false"})
	writeSkill(t, user, "zeta", "z skill\nsecond line desc", "z", nil)
	// project shadows user on name collision
	writeSkill(t, user, "dupe", "user version", "u", nil)
	writeSkill(t, proj, "dupe", "PROJECT version", "p", nil)
	catalog, err := Discover(user, proj, &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	idx := catalog.RenderIndex()
	if !strings.HasPrefix(idx, "<available_skills>") {
		t.Fatalf("index missing opening tag: %q", idx)
	}
	if strings.Contains(idx, "hidden") {
		t.Fatalf("index includes hidden skill: %q", idx)
	}
	if strings.Contains(idx, "user version") {
		t.Fatalf("index used user version instead of shadowing project: %q", idx)
	}
	if !strings.Contains(idx, "PROJECT version") {
		t.Fatalf("index missing project description: %q", idx)
	}
}

func TestRenderIndexEmpty(t *testing.T) {
	t.Parallel()
	catalog, err := Discover(t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if got := catalog.RenderIndex(); got != "" {
		t.Fatalf("RenderIndex on empty catalog = %q, want empty", got)
	}
}

func TestRenderIndexAllHidden(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkillMeta(t, user, "s1", "desc", "body", nil, []string{"model-invocable: false"})
	catalog, err := Discover(user, t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if got := catalog.RenderIndex(); got != "" {
		t.Fatalf("RenderIndex with all hidden = %q, want empty", got)
	}
	if len(catalog.Names()) != 1 {
		t.Fatalf("hidden skill should stay human-invocable; names = %v", catalog.Names())
	}
}

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
