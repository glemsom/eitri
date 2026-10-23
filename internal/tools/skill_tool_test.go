package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/skillindex"
)

func discoverOne(t *testing.T, name, body string, files map[string]string) *Catalog {
	t.Helper()
	user := t.TempDir()
	writeSkill(t, user, name, "a test skill", body, files)
	catalog, err := Discover(user, t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	return catalog
}

func runSkill(t *testing.T, rd *Registry, ctx context.Context, args map[string]any) (string, error) {
	t.Helper()
	res, err := rd.Run(ctx, "skill", args)
	return res.Text, err
}

func TestSkillToolExposedToProvider(t *testing.T) {
	t.Parallel()
	rd, _ := NewRegistry(testDeps(t, "", discoverOne(t, "s1", "body", nil)))
	if !contains(rd.Names(), "skill") {
		t.Fatalf("registry missing the skill tool; names = %v", rd.Names())
	}
	var def *Definition
	for _, d := range rd.Definitions() {
		if d.Name == "skill" {
			def = &d
		}
	}
	if def == nil {
		t.Fatalf("skill definition not exposed to the provider: %v", rd.Definitions())
	}
	if def.Parameters["additionalProperties"] != false {
		t.Fatalf("skill schema additionalProperties = %v, want false", def.Parameters["additionalProperties"])
	}
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("skill schema properties = %T, want an object", def.Parameters["properties"])
	}
	if _, ok := props["name"]; !ok {
		t.Fatalf("skill schema lacks the name property: %v", props)
	}
	required, _ := def.Parameters["required"].([]string)
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("skill schema required = %v, want [name]", def.Parameters["required"])
	}
	if def.Description == "" {
		t.Fatal("skill description is empty; the activation mechanics belong there")
	}
	for _, want := range []string{"already active", "directory", "SKILL.md"} {
		if !strings.Contains(def.Description, want) {
			t.Errorf("skill description lacks activation mechanic %q:\n%s", want, def.Description)
		}
	}
}

// skillNameProperty returns the `skill` tool's schema property for its `name`
// parameter, failing the test if the tool is not exposed.
func skillNameProperty(t *testing.T, rd *Registry) map[string]any {
	t.Helper()
	for _, d := range rd.Definitions() {
		if d.Name != "skill" {
			continue
		}
		props, ok := d.Parameters["properties"].(map[string]any)
		if !ok {
			t.Fatalf("skill schema properties = %T, want an object", d.Parameters["properties"])
		}
		prop, ok := props["name"].(map[string]any)
		if !ok {
			t.Fatalf("skill schema name property = %T, want an object", props["name"])
		}
		return prop
	}
	t.Fatalf("skill definition not exposed to the provider: %v", rd.Definitions())
	return nil
}

func TestSkillToolNameEnumMatchesModelVisibleSkills(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkill(t, user, "b-skill", "b desc", "b", nil)
	writeSkillMeta(t, user, "hidden", "h", "body", nil, []string{"model-invocable: false"})
	writeSkill(t, user, "a-skill", "a desc", "a", nil)
	catalog, err := Discover(user, t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd, _ := NewRegistry(testDeps(t, "", catalog))

	enum, ok := skillNameProperty(t, rd)["enum"].([]string)
	if !ok {
		t.Fatalf("skill schema name has no string enum; got %v", skillNameProperty(t, rd)["enum"])
	}
	if want := []string{"a-skill", "b-skill"}; !slices.Equal(enum, want) {
		t.Fatalf("skill name enum = %v, want %v", enum, want)
	}
	if !slices.Equal(enum, catalog.ModelVisibleSkills()) {
		t.Fatalf("skill name enum = %v, want the model-visible set %v", enum, catalog.ModelVisibleSkills())
	}
	if slices.Contains(enum, "hidden") {
		t.Fatalf("skill name enum leaks the human-only skill: %v", enum)
	}
}

func TestSkillToolNameUnconstrainedWithoutModelVisibleSkills(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkillMeta(t, user, "hidden", "h", "body", nil, []string{"model-invocable: false"})
	catalog, err := Discover(user, t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd, _ := NewRegistry(testDeps(t, "", catalog))

	if !contains(rd.Names(), "skill") {
		t.Fatalf("registry missing the skill tool with no model-visible skills; names = %v", rd.Names())
	}
	prop := skillNameProperty(t, rd)
	if _, ok := prop["enum"]; ok {
		t.Fatalf("skill name carries an enum with no model-visible skills: %v", prop["enum"])
	}
	if prop["type"] != "string" {
		t.Fatalf("skill name type = %v, want string", prop["type"])
	}
}

func TestSkillToolActivatesModelInvocableSkill(t *testing.T) {
	t.Parallel()
	catalog := discoverOne(t, "res", "## Instructions\n\nuse the script", map[string]string{"scripts/run.sh": "#!/bin/sh\n"})
	sk := catalog.Skill("res")
	rd, _ := NewRegistry(testDeps(t, "", catalog))

	out, err := runSkill(t, rd, context.Background(), map[string]any{"name": "res"})
	if err != nil {
		t.Fatalf("skill tool error = %v, want nil", err)
	}
	if want := skillindex.ActivationOpen("res", sk.Dir); !strings.Contains(out, want) {
		t.Fatalf("payload missing directory-tagged content block %q:\n%s", want, out)
	}
	if !strings.Contains(out, "use the script") || !strings.Contains(out, "scripts/run.sh") {
		t.Fatalf("payload missing body or resources:\n%s", out)
	}
}

func TestSkillToolShortCircuitsActiveSkill(t *testing.T) {
	t.Parallel()
	catalog := discoverOne(t, "res", "UNIQUE-BODY-MARKER", nil)
	sk := catalog.Skill("res")
	rd, _ := NewRegistry(testDeps(t, "", catalog))

	ctx := skillindex.WithActiveSkills(context.Background(), []string{"res"})
	out, err := runSkill(t, rd, ctx, map[string]any{"name": "res"})
	if err != nil {
		t.Fatalf("skill tool error = %v, want nil", err)
	}
	if strings.Contains(out, "UNIQUE-BODY-MARKER") {
		t.Fatalf("already-active activation re-injected the body:\n%s", out)
	}
	if skillindex.HasActivation(out) {
		t.Fatalf("already-active activation returned a full content block:\n%s", out)
	}
	if !strings.Contains(out, sk.Dir) {
		t.Fatalf("already-active note does not repeat the skill directory %q:\n%s", sk.Dir, out)
	}
}

func TestSkillToolClaimsNameOncePerContext(t *testing.T) {
	t.Parallel()
	catalog := discoverOne(t, "res", "UNIQUE-BODY-MARKER", nil)
	sk := catalog.Skill("res")
	rd, _ := NewRegistry(testDeps(t, "", catalog))

	ctx := skillindex.WithActiveSkills(context.Background(), nil)
	var payloads, notes int
	for range 2 {
		out, err := runSkill(t, rd, ctx, map[string]any{"name": "res"})
		if err != nil {
			t.Fatalf("skill tool error = %v, want nil", err)
		}
		switch {
		case strings.Contains(out, "UNIQUE-BODY-MARKER"):
			payloads++
		case strings.Contains(out, sk.Dir) && strings.Contains(out, "already active"):
			notes++
		}
	}
	if payloads != 1 || notes != 1 {
		t.Fatalf("same-turn claims: payloads = %d, notes = %d, want 1 and 1", payloads, notes)
	}
}

func TestSkillToolRejectsHumanOnlySkill(t *testing.T) {
	t.Parallel()
	user := t.TempDir()
	writeSkillMeta(t, user, "human-only", "hidden from the model", "body", nil, []string{"model-invocable: false"})
	catalog, err := Discover(user, t.TempDir(), t.TempDir(), &warningSink{})
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	rd, _ := NewRegistry(testDeps(t, "", catalog))
	if _, err := runSkill(t, rd, context.Background(), map[string]any{"name": "human-only"}); err == nil {
		t.Fatal("skill tool activating a human-only skill = nil error, want an error")
	}
}

func TestSkillToolRejectsUnknownSkill(t *testing.T) {
	t.Parallel()
	rd, _ := NewRegistry(testDeps(t, "", discoverOne(t, "known", "body", nil)))
	if _, err := runSkill(t, rd, context.Background(), map[string]any{"name": "nope"}); err == nil {
		t.Fatal("skill tool activating an unknown skill = nil error, want an error")
	}
}

func TestSkillToolRequiresName(t *testing.T) {
	t.Parallel()
	rd, _ := NewRegistry(testDeps(t, "", discoverOne(t, "known", "body", nil)))
	if _, err := runSkill(t, rd, context.Background(), map[string]any{}); err == nil {
		t.Fatal("skill tool without a name = nil error, want an error")
	}
}

// TestSkillPayloadCarriesDirectory guards the shared renderer: the human slash
// path and the model tool must emit the same directory-tagged block.
func TestSkillPayloadCarriesDirectory(t *testing.T) {
	t.Parallel()
	catalog := discoverOne(t, "res", "body", nil)
	sk := catalog.Skill("res")
	rd, _ := NewRegistry(testDeps(t, "", catalog))
	res, err := rd.ActivateSkill(context.Background(), "res")
	if err != nil {
		t.Fatalf("ActivateSkill error = %v, want nil", err)
	}
	if want := skillindex.ActivationOpen("res", sk.Dir); !strings.Contains(res.Text, want) {
		t.Fatalf("human activation payload missing %q:\n%s", want, res.Text)
	}
}
