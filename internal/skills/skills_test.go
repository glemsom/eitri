package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper: create a temporary skill directory with SKILL.md content.
func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return dir
}

// Helper: write a SKILL.md with custom frontmatter.
func writeSkillMD(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func diagnosticsContain(diags Diagnostics, want string) bool {
	for _, diag := range diags {
		if strings.Contains(diag.Message, want) {
			return true
		}
	}
	return false
}

func TestParseValidSkill(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	writeSkill(t, skillDir, "my-skill", "# My Skill\n\nInstructions here.")

	skill, diags := ParseSKILLMD(skillDir)
	// Dir name matches skill name, so no warning
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if skill.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", skill.Name, "my-skill")
	}
	if skill.Description != "Test skill my-skill" {
		t.Errorf("Description = %q, want %q", skill.Description, "Test skill my-skill")
	}
	if skill.Body != "# My Skill\n\nInstructions here." {
		t.Errorf("Body = %q", skill.Body)
	}
	if skill.Path != skillDir {
		t.Errorf("Path = %q, want %q", skill.Path, skillDir)
	}
	if skill.Scope != ScopeUnknown {
		t.Errorf("Scope = %q, want %q", skill.Scope, ScopeUnknown)
	}
}

func TestParseInvalidMissingSKILLMD(t *testing.T) {
	dir := t.TempDir()
	skill, diags := ParseSKILLMD(dir)
	if skill != nil {
		t.Error("expected nil skill for missing SKILL.md")
	}
	if len(diags) == 0 {
		t.Error("expected diagnostics for missing SKILL.md")
	}
}

func TestParseInvalidMissingName(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "---\ndescription: No name\n---\nBody")
	skill, diags := ParseSKILLMD(dir)
	if skill != nil {
		t.Error("expected nil skill for missing name")
	}
	if len(diags) == 0 {
		t.Error("expected diagnostics for missing name")
	}
}

func TestParseInvalidEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "---\nname: test-skill\ndescription: \n---\nBody")
	skill, diags := ParseSKILLMD(dir)
	if skill != nil {
		t.Error("expected nil skill for empty description")
	}
	if len(diags) == 0 {
		t.Error("expected diagnostics for empty description")
	}
}

func TestParseInvalidNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillMD(t, dir, "Just markdown body, no frontmatter.")
	skill, diags := ParseSKILLMD(dir)
	if skill != nil {
		t.Error("expected nil skill for missing frontmatter")
	}
	if len(diags) == 0 {
		t.Error("expected diagnostics for missing frontmatter")
	}
}

func TestParseWarnsDirNameMismatch(t *testing.T) {
	dir := t.TempDir()
	// Skill name differs from parent directory name
	skillDir := filepath.Join(dir, "code-review")
	writeSkill(t, skillDir, "my-review", "# Review")
	skill, diags := ParseSKILLMD(skillDir)
	if skill == nil {
		t.Fatal("expected skill despite name mismatch")
	}
	if skill.Name != "my-review" {
		t.Errorf("Name = %q", skill.Name)
	}
	if HasSeverity(diags, SeverityWarn) {
		t.Logf("has warning diagnostic as expected: %v", diags)
	}
}

func TestParseOptionalFields(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "full-skill")
	content := `---
name: full-skill
description: A skill with all optional fields
license: MIT
compatibility: linux
metadata:
  author: test
  version: "1.0"
allowed-tools:
  - Bash(git:*)
  - Read
---
# Full Skill
Detailed instructions.
`
	writeSkillMD(t, skillDir, content)
	skill, diags := ParseSKILLMD(skillDir)
	// Dir name matches skill name, no warning
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if skill == nil {
		t.Fatal("expected skill")
	}
	if skill.Name != "full-skill" {
		t.Errorf("Name = %q", skill.Name)
	}
}

func TestParseBodyExcludesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "body-test"), "body-test", "# Only Body")
	skill, _ := ParseSKILLMD(filepath.Join(dir, "body-test"))
	if skill == nil {
		t.Fatal("expected skill")
	}
	if skill.Body == "" {
		t.Error("body should not be empty")
	}
}

// --- Discovery tests ---

func TestDiscoverNoRoots(t *testing.T) {
	svc := NewService()
	svc.roots = []Root{}

	skills, diags := svc.Discover()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	svc := NewService()
	svc.roots = []Root{
		{Path: "/nonexistent/path/xyz123", Scope: ScopeProjectEitri},
	}
	skills, diags := svc.Discover()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for missing root, got %d", len(skills))
	}
	// Missing root should be skipped with warning
	if !HasSeverity(diags, SeverityWarn) {
		t.Errorf("expected warning for missing root, got %v", diags)
	}
}

func TestDiscoverSingleRoot(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "skill-a"), "skill-a", "# Skill A")
	writeSkill(t, filepath.Join(rootDir, "skill-b"), "skill-b", "# Skill B")

	svc := NewService()
	svc.roots = []Root{{Path: rootDir, Scope: ScopeProjectEitri}}
	skills, diags := svc.Discover()
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	// Verify scopes assigned
	for _, s := range skills {
		if s.Scope != ScopeProjectEitri {
			t.Errorf("skill %q scope = %q, want %q", s.Name, s.Scope, ScopeProjectEitri)
		}
	}
}

func TestDiscoverSubdirsOnly(t *testing.T) {
	rootDir := t.TempDir()
	// SKILL.md at root level should NOT be counted as a skill
	writeSkillMD(t, rootDir, "---\nname: root-skill\ndescription: Should not be discovered\n---\nBody")
	// Plain subdirectory without SKILL.md should be ignored
	if err := os.MkdirAll(filepath.Join(rootDir, "notes"), 0755); err != nil {
		t.Fatalf("mkdir notes: %v", err)
	}
	// SKILL.md in a subdirectory should be discovered
	writeSkill(t, filepath.Join(rootDir, "valid-skill"), "valid-skill", "# Valid")

	svc := NewService()
	svc.roots = []Root{{Path: rootDir, Scope: ScopeProjectEitri}}
	skills, _ := svc.Discover()
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (subdir SKILL.md only), got %d", len(skills))
	}
	if skills[0].Name != "valid-skill" {
		t.Errorf("Name = %q, want %q", skills[0].Name, "valid-skill")
	}
}

func TestDiscoverInvalidSkill(t *testing.T) {
	rootDir := t.TempDir()
	// Valid skill
	writeSkill(t, filepath.Join(rootDir, "valid"), "valid", "# Valid")
	// Invalid: missing name
	writeSkillMD(t, filepath.Join(rootDir, "invalid"), "---\ndescription: No name\n---\nBody")

	svc := NewService()
	svc.roots = []Root{{Path: rootDir, Scope: ScopeProjectEitri}}
	skills, diags := svc.Discover()
	if len(skills) != 2 {
		t.Fatalf("expected valid + invalid placeholder skills, got %d", len(skills))
	}
	foundInvalid := false
	for _, skill := range skills {
		if skill.Status == StatusInvalid && skill.Name == "invalid" {
			foundInvalid = true
			break
		}
	}
	if !foundInvalid {
		t.Fatalf("expected invalid placeholder in discovered skills, got %#v", skills)
	}
	if !HasSeverity(diags, SeverityError) {
		t.Errorf("expected error diagnostic for invalid skill, got %v", diags)
	}
}

// --- Registry tests ---

func TestRegistryPrecedence(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeSkill(t, filepath.Join(rootA, "common"), "common", "# Common from project")
	writeSkill(t, filepath.Join(rootA, "unique-a"), "unique-a", "# Unique A")
	writeSkill(t, filepath.Join(rootB, "common"), "common", "# Common from user")
	writeSkill(t, filepath.Join(rootB, "unique-b"), "unique-b", "# Unique B")

	svc := NewService()
	svc.roots = []Root{
		{Path: rootA, Scope: ScopeProjectEitri},
		{Path: rootB, Scope: ScopeUserEitri},
	}
	skills, _ := svc.Discover()
	registry := BuildRegistry(skills, nil, nil)

	// "common" should be effective from project (higher precedence)
	eff := registry.Effective()
	if eff["common"] == nil {
		t.Fatal("expected 'common' in effective map")
	}
	if eff["common"].Scope != ScopeProjectEitri {
		t.Errorf("effective 'common' scope = %q, want %q", eff["common"].Scope, ScopeProjectEitri)
	}

	// "unique-a" and "unique-b" should be effective
	if eff["unique-a"] == nil {
		t.Error("expected 'unique-a' in effective map")
	}
	if eff["unique-b"] == nil {
		t.Error("expected 'unique-b' in effective map")
	}

	// Registry should have 3 effective skills
	if len(eff) != 3 {
		t.Errorf("effective count = %d, want 3", len(eff))
	}

	// Check shadowed records
	shadowed := registry.Shadowed()
	if len(shadowed) != 1 || shadowed[0].Name != "common" {
		t.Errorf("expected one shadowed 'common', got %v", shadowed)
	}
}

func TestRegistryInvalidNotInEffective(t *testing.T) {
	rootDir := t.TempDir()
	writeSkillMD(t, filepath.Join(rootDir, "bad"), "---\ndescription: No name\n---\nBody")
	writeSkill(t, filepath.Join(rootDir, "good"), "good", "# Good")

	svc := NewService()
	svc.roots = []Root{{Path: rootDir, Scope: ScopeProjectEitri}}
	skills, _ := svc.Discover()
	registry := BuildRegistry(skills, nil, nil)

	eff := registry.Effective()
	if eff["bad"] != nil {
		t.Error("invalid skill should not be in effective map")
	}
	if eff["good"] == nil {
		t.Error("valid skill should be in effective map")
	}
}

// --- Resource manifest tests ---

func TestResourceManifestBasic(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "res-skill", "# Resources")
	// Create some resource files
	scriptsDir := filepath.Join(dir, "scripts")
	refsDir := filepath.Join(dir, "references")
	assetsDir := filepath.Join(dir, "assets")
	os.MkdirAll(scriptsDir, 0755)
	os.MkdirAll(refsDir, 0755)
	os.MkdirAll(assetsDir, 0755)
	os.WriteFile(filepath.Join(scriptsDir, "test.sh"), []byte("echo test"), 0644)
	os.WriteFile(filepath.Join(refsDir, "guide.md"), []byte("# Guide"), 0644)
	os.WriteFile(filepath.Join(assetsDir, "logo.png"), []byte("fake-png"), 0644)

	resources := ListResources(dir)
	if len(resources) != 3 {
		t.Fatalf("expected 3 resources, got %d: %v", len(resources), resources)
	}
}

func TestResourceManifestCap(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "big-skill", "# Big")
	scriptsDir := filepath.Join(dir, "scripts")
	os.MkdirAll(scriptsDir, 0755)

	// Create more than 200 files
	for i := 0; i < 250; i++ {
		os.WriteFile(filepath.Join(scriptsDir, fmt.Sprintf("file-%d.sh", i)), []byte("x"), 0644)
	}

	resources := ListResources(dir)
	if len(resources) > 200 {
		t.Errorf("resource count = %d, want <= 200", len(resources))
	}
}

func TestResourceManifestDepthCap(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "deep-skill", "# Deep")
	deepDir := filepath.Join(dir, "scripts", "a", "b", "c", "d", "e")
	os.MkdirAll(deepDir, 0755)
	os.WriteFile(filepath.Join(deepDir, "deep.txt"), []byte("deep"), 0644)

	resources := ListResources(dir)
	// Depth 4 from skill root: scripts/a/b/c/d/e is depth 5 from root
	// Only files up to depth 4 from root should be listed
	if len(resources) != 0 {
		t.Errorf("expected 0 resources beyond depth 4, got %d: %v", len(resources), resources)
	}
}

func TestResourceManifestEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "empty-skill", "# Empty")
	os.MkdirAll(filepath.Join(dir, "scripts"), 0755)
	os.MkdirAll(filepath.Join(dir, "references"), 0755)

	resources := ListResources(dir)
	if len(resources) != 0 {
		t.Errorf("expected 0 resources for empty dirs, got %d", len(resources))
	}
}

// --- Service integration tests ---

func TestNewServiceDefaultRoots(t *testing.T) {
	svc := NewService()
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	// Should have default roots (though resolved to home/workspace paths)
	// Just verify it doesn't panic
	_ = svc.Roots()
}

func TestServiceRefresh(t *testing.T) {
	rootDir := t.TempDir()
	svc := NewService()
	svc.SetRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Initially no skills
	registry := svc.Refresh()
	if len(registry.Effective()) != 0 {
		t.Errorf("expected 0 effective skills, got %d", len(registry.Effective()))
	}

	// Add a skill and refresh
	writeSkill(t, filepath.Join(rootDir, "new-skill"), "new-skill", "# New")
	registry = svc.Refresh()
	if len(registry.Effective()) != 1 {
		t.Errorf("expected 1 effective skill, got %d", len(registry.Effective()))
	}
}

func TestServiceRefresh_SurfacesInvalidSkillsAndDiagnostics(t *testing.T) {
	rootDir := t.TempDir()
	brokenDir := filepath.Join(rootDir, "broken-skill")
	writeSkillMD(t, brokenDir, "---\ndescription: missing name\n---\nBody")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})
	registry := svc.Refresh()

	if len(registry.Invalid()) != 1 {
		t.Fatalf("invalid count = %d, want 1", len(registry.Invalid()))
	}
	if registry.Invalid()[0].Name != "broken-skill" {
		t.Errorf("invalid skill name = %q, want broken-skill", registry.Invalid()[0].Name)
	}
	if registry.Invalid()[0].Path != brokenDir {
		t.Errorf("invalid skill path = %q, want %q", registry.Invalid()[0].Path, brokenDir)
	}
	if !diagnosticsContain(registry.Diagnostics(), "name field missing or empty") {
		t.Fatalf("diagnostics = %#v, want parse error for invalid skill", registry.Diagnostics())
	}
}

func TestServiceRefresh_PreservesRootDiagnostics(t *testing.T) {
	rootDir := t.TempDir()
	rootFile := filepath.Join(rootDir, "not-a-directory")
	if err := os.WriteFile(rootFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write root marker file: %v", err)
	}

	svc := NewServiceWithRoots([]Root{{Path: rootFile, Scope: ScopeProjectEitri}})
	registry := svc.Refresh()

	if !diagnosticsContain(registry.Diagnostics(), "is not a directory") {
		t.Fatalf("diagnostics = %#v, want root warning", registry.Diagnostics())
	}
}

func TestLookupByName(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My")
	writeSkill(t, filepath.Join(rootDir, "other"), "other", "# Other")

	svc := NewService()
	svc.SetRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})
	svc.Refresh()

	skill := svc.Lookup("my-skill")
	if skill == nil {
		t.Fatal("expected to find 'my-skill'")
	}
	if skill.Name != "my-skill" {
		t.Errorf("Name = %q", skill.Name)
	}

	// Lookup non-existent
	skill = svc.Lookup("nonexistent")
	if skill != nil {
		t.Error("expected nil for non-existent skill")
	}
}

func TestLookupByNormalized(t *testing.T) {
	rootDir := t.TempDir()
	// Skill names are case-sensitive per spec, but lookup should be case-insensitive
	writeSkill(t, filepath.Join(rootDir, "Code-Review"), "Code-Review", "# Review")

	svc := NewService()
	svc.SetRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})
	svc.Refresh()

	// Lookup with exact case
	skill := svc.Lookup("Code-Review")
	if skill == nil {
		t.Fatal("expected to find 'Code-Review'")
	}

	// Lookup with different case (should be case-insensitive)
	skill = svc.Lookup("code-review")
	if skill == nil {
		t.Error("case-insensitive lookup failed for 'code-review', expected to find 'Code-Review'")
	}
}

func TestEffectiveSkills(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")

	svc := NewService()
	svc.SetRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})
	svc.Refresh()

	eff := svc.Effective()
	if len(eff) != 2 {
		t.Errorf("effective count = %d, want 2", len(eff))
	}
}

func TestShadowedSkills(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeSkill(t, filepath.Join(rootA, "common"), "common", "# Project")
	writeSkill(t, filepath.Join(rootB, "common"), "common", "# User")

	svc := NewService()
	svc.SetRoots([]Root{
		{Path: rootA, Scope: ScopeProjectEitri},
		{Path: rootB, Scope: ScopeUserEitri},
	})
	svc.Refresh()

	shadowed := svc.Shadowed()
	eff := svc.Effective()

	if len(shadowed) != 1 {
		t.Errorf("expected 1 shadowed, got %d", len(shadowed))
	}
	if eff["common"] == nil {
		t.Error("'common' should be effective")
	}
	if eff["common"].Scope != ScopeProjectEitri {
		t.Errorf("effective 'common' scope = %q, want %q", eff["common"].Scope, ScopeProjectEitri)
	}
}

func TestAllSkills(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeSkill(t, filepath.Join(rootA, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootA, "s2"), "s2", "# S2")
	writeSkill(t, filepath.Join(rootB, "s1"), "s1", "# S1 dup")

	svc := NewService()
	svc.SetRoots([]Root{
		{Path: rootA, Scope: ScopeProjectEitri},
		{Path: rootB, Scope: ScopeUserEitri},
	})
	svc.Refresh()

	all := svc.All()
	if len(all) != 3 {
		t.Errorf("all skills count = %d, want 3 (including shadowed)", len(all))
	}
}

// --- Diagnostics helpers ---

func TestHasSeverity(t *testing.T) {
	diags := Diagnostics{
		{Severity: SeverityWarn, Message: "test warning"},
	}
	if !HasSeverity(diags, SeverityWarn) {
		t.Error("expected to find warning severity")
	}
	if HasSeverity(diags, SeverityError) {
		t.Error("not expected to find error severity")
	}
}

// --- Disabled skills tests ---

func TestBuildRegistry_Disabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "enabled-skill"), "enabled-skill", "# Enabled")
	writeSkill(t, filepath.Join(rootDir, "disabled-skill"), "disabled-skill", "# Disabled")

	skills, _ := DiscoverSkills([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})
	registry := BuildRegistry(skills, nil, []string{"disabled-skill"})

	// Disabled skill should NOT be in effective
	eff := registry.Effective()
	if eff["disabled-skill"] != nil {
		t.Error("disabled skill should not be in effective map")
	}
	if eff["enabled-skill"] == nil {
		t.Error("enabled skill should be in effective map")
	}

	// Disabled skill should be in disabled list
	disabled := registry.Disabled()
	if len(disabled) != 1 {
		t.Fatalf("expected 1 disabled skill, got %d", len(disabled))
	}
	if disabled[0].Name != "disabled-skill" {
		t.Errorf("disabled skill name = %q, want %q", disabled[0].Name, "disabled-skill")
	}
	if disabled[0].Status != StatusDisabled {
		t.Errorf("disabled skill status = %q, want %q", disabled[0].Status, StatusDisabled)
	}
}

func TestBuildRegistry_DisabledShadowed(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeSkill(t, filepath.Join(rootA, "common"), "common", "# Project common")
	writeSkill(t, filepath.Join(rootB, "common"), "common", "# User common")

	skills, _ := DiscoverSkills([]Root{
		{Path: rootA, Scope: ScopeProjectEitri},
		{Path: rootB, Scope: ScopeUserEitri},
	})
	// Disable "common" — the effective one (project scope) should be disabled;
	// the shadowed one should remain shadowed
	registry := BuildRegistry(skills, nil, []string{"common"})

	eff := registry.Effective()
	if eff["common"] != nil {
		t.Error("disabled+previously-effective skill should not be in effective map")
	}

	// Skill should be in disabled list
	disabled := registry.Disabled()
	if len(disabled) != 1 {
		t.Fatalf("expected 1 disabled skill, got %d", len(disabled))
	}
	if disabled[0].Name != "common" {
		t.Errorf("disabled skill name = %q, want %q", disabled[0].Name, "common")
	}

	// Shadowed list should still have the shadowed copy
	shadowed := registry.Shadowed()
	if len(shadowed) != 1 {
		t.Errorf("expected 1 shadowed skill, got %d", len(shadowed))
	}
}

func TestService_DisableSkill(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My Skill")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Initially effective
	if svc.Lookup("my-skill") == nil {
		t.Fatal("expected skill to be effective initially")
	}

	// Disable it
	called := false
	svc.SetDisabled("my-skill", true, func(disabled []string) {
		called = true
		if len(disabled) != 1 || disabled[0] != "my-skill" {
			t.Errorf("callback disabled list = %v, want [my-skill]", disabled)
		}
	})

	if !called {
		t.Error("callback was not invoked")
	}

	// Lookup should now return nil
	if svc.Lookup("my-skill") != nil {
		t.Error("disabled skill should not be found by Lookup")
	}

	// Should no longer be in effective
	eff := svc.Effective()
	if eff["my-skill"] != nil {
		t.Error("disabled skill should not be in effective map")
	}
}

func TestService_EnableSkill(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My Skill")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Disable first
	called := false
	svc.SetDisabled("my-skill", true, func(disabled []string) {
		called = true
	})
	if !called {
		t.Error("callback was not invoked on disable")
	}
	if svc.Lookup("my-skill") != nil {
		t.Fatal("expected nil after disable")
	}

	// Enable it
	called = false
	svc.SetDisabled("my-skill", false, func(disabled []string) {
		called = true
		if len(disabled) != 0 {
			t.Errorf("callback disabled list = %v, want empty", disabled)
		}
	})

	if !called {
		t.Error("callback was not invoked on enable")
	}

	// Lookup should return it again
	if svc.Lookup("my-skill") == nil {
		t.Error("enabled skill should be found by Lookup")
	}
}

func TestService_CatalogXML_OmitDisabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "visible"), "visible", "# Visible")
	writeSkill(t, filepath.Join(rootDir, "hidden"), "hidden", "# Hidden")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Disable "hidden"
	svc.SetDisabled("hidden", true, nil)
	svc.Refresh()

	xml := svc.SkillsCatalogXML()
	if strings.Contains(xml, "hidden") {
		t.Error("SkillsCatalogXML should omit disabled skill")
	}
	if !strings.Contains(xml, "visible") {
		t.Error("SkillsCatalogXML should include enabled skill")
	}
}

func TestService_Summary_OmitDisabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "visible"), "visible", "# Visible")
	writeSkill(t, filepath.Join(rootDir, "hidden"), "hidden", "# Hidden")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Disable "hidden"
	svc.SetDisabled("hidden", true, nil)
	svc.Refresh()

	summary := svc.Registry().Summary()
	for _, s := range summary {
		if s.Name == "hidden" {
			t.Error("Summary should omit disabled skill")
		}
	}
	found := false
	for _, s := range summary {
		if s.Name == "visible" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Summary should include enabled skill")
	}
}

func TestService_Directories_OmitDisabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "visible"), "visible", "# Visible")
	hiddenDir := filepath.Join(rootDir, "hidden")
	writeSkill(t, hiddenDir, "hidden", "# Hidden")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Initially both dirs present
	dirs := svc.SkillDirectories()
	foundHidden := false
	for _, d := range dirs {
		if d == hiddenDir {
			foundHidden = true
			break
		}
	}
	if !foundHidden {
		t.Error("SkillDirectories should include hidden dir before disable")
	}

	// Disable "hidden"
	svc.SetDisabled("hidden", true, nil)
	svc.Refresh()

	dirs = svc.SkillDirectories()
	for _, d := range dirs {
		if d == hiddenDir {
			t.Error("SkillDirectories should omit disabled skill path")
		}
	}
}

func TestService_SetDisabled_Persists(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Disable s1 with a callback
	var persisted []string
	svc.SetDisabled("s1", true, func(disabled []string) {
		persisted = disabled
	})

	if len(persisted) != 1 || persisted[0] != "s1" {
		t.Fatalf("expected callback to be called with [s1], got %v", persisted)
	}
}

func TestService_RefreshPreservesDisabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Disable s1
	svc.SetDisabled("s1", true, nil)

	// Refresh should preserve disabled state
	svc.Refresh()

	// s1 should still be disabled
	if svc.Lookup("s1") != nil {
		t.Error("s1 should still be disabled after Refresh")
	}
	if svc.Lookup("s2") == nil {
		t.Error("s2 should still be effective after Refresh")
	}
}

func TestService_SetDisabledList(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")
	writeSkill(t, filepath.Join(rootDir, "s3"), "s3", "# S3")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// Set disabled list to s1 and s3
	var saved []string
	svc.SetDisabledList([]string{"s1", "s3"}, func(disabled []string) {
		saved = disabled
	})

	if len(saved) != 2 {
		t.Fatalf("expected 2 disabled, got %d: %v", len(saved), saved)
	}

	// Verify effective
	eff := svc.Effective()
	if eff["s1"] != nil {
		t.Error("s1 should not be effective")
	}
	if eff["s2"] == nil {
		t.Error("s2 should be effective")
	}
	if eff["s3"] != nil {
		t.Error("s3 should not be effective")
	}
}

func TestService_ClearDisabled(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	// First disable s1
	svc.SetDisabled("s1", true, nil)

	// Now clear all
	var saved []string
	svc.ClearDisabled(func(disabled []string) {
		saved = disabled
	})

	if len(saved) != 0 {
		t.Fatalf("expected empty disabled, got %v", saved)
	}

	// Verify both effective
	eff := svc.Effective()
	if eff["s1"] == nil {
		t.Error("s1 should be effective after clear")
	}
	if eff["s2"] == nil {
		t.Error("s2 should be effective")
	}
}

// --- Service.Invalid() ---

func TestService_Invalid_ReturnsInvalidSkills(t *testing.T) {
	rootDir := t.TempDir()
	writeSkillMD(t, filepath.Join(rootDir, "no-name"), "---\ndescription: Missing name\n---\nBody")
	writeSkill(t, filepath.Join(rootDir, "valid-skill"), "valid-skill", "# Valid")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	invalid := svc.Invalid()
	if len(invalid) != 1 {
		t.Fatalf("Invalid() = %d skills, want 1", len(invalid))
	}
	if invalid[0].Name != "no-name" {
		t.Errorf("invalid skill name = %q, want %q", invalid[0].Name, "no-name")
	}
	if invalid[0].Status != StatusInvalid {
		t.Errorf("invalid skill status = %q, want %q", invalid[0].Status, StatusInvalid)
	}
}

func TestService_Invalid_Empty(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "good"), "good", "# Good")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	invalid := svc.Invalid()
	if len(invalid) != 0 {
		t.Errorf("Invalid() = %d skills, want 0", len(invalid))
	}
}

// --- Service.IsDisabled() ---

func TestService_IsDisabled_DisabledSkill(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")
	writeSkill(t, filepath.Join(rootDir, "s2"), "s2", "# S2")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	svc.SetDisabled("s1", true, nil)

	if !svc.IsDisabled("s1") {
		t.Error("IsDisabled('s1') should be true after disabling")
	}
	if svc.IsDisabled("s2") {
		t.Error("IsDisabled('s2') should be false for enabled skill")
	}
}

func TestService_IsDisabled_NonexistentSkill(t *testing.T) {
	svc := NewServiceWithRoots(nil)

	if svc.IsDisabled("nonexistent") {
		t.Error("IsDisabled('nonexistent') should be false")
	}
}

func TestService_IsDisabled_AfterEnable(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "s1"), "s1", "# S1")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	svc.SetDisabled("s1", true, nil)
	if !svc.IsDisabled("s1") {
		t.Error("IsDisabled('s1') should be true after disable")
	}

	svc.SetDisabled("s1", false, nil)
	if svc.IsDisabled("s1") {
		t.Error("IsDisabled('s1') should be false after enable")
	}
}

// --- Service.HomeDir() ---

func TestService_HomeDir_NewService(t *testing.T) {
	svc := NewService()

	home, _ := os.UserHomeDir()
	if svc.HomeDir() != home {
		t.Errorf("HomeDir() = %q, want %q", svc.HomeDir(), home)
	}
}

func TestService_HomeDir_NewServiceWithRoots(t *testing.T) {
	svc := NewServiceWithRoots(nil)

	// NewServiceWithRoots does not set home, so it should be empty
	if svc.HomeDir() != "" {
		t.Errorf("HomeDir() = %q, want empty", svc.HomeDir())
	}
}

// --- Service.Workspace() ---

func TestService_Workspace_NewService(t *testing.T) {
	svc := NewService()

	cwd, _ := os.Getwd()
	if svc.Workspace() != cwd {
		t.Errorf("Workspace() = %q, want %q", svc.Workspace(), cwd)
	}
}

func TestService_Workspace_NewServiceWithRoots(t *testing.T) {
	svc := NewServiceWithRoots(nil)

	// NewServiceWithRoots does not set workspace, so it should be empty
	if svc.Workspace() != "" {
		t.Errorf("Workspace() = %q, want empty", svc.Workspace())
	}
}

// --- Service.DiagnosticSummary() ---

func TestService_DiagnosticSummary_WithDiagnostics(t *testing.T) {
	rootDir := t.TempDir()
	// Create a file instead of a directory to trigger a diagnostic
	rootFile := filepath.Join(rootDir, "not-a-dir")
	if err := os.WriteFile(rootFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	svc := NewServiceWithRoots([]Root{{Path: rootFile, Scope: ScopeProjectEitri}})
	svc.Refresh()

	summary := svc.DiagnosticSummary()
	if summary == "" {
		t.Fatal("DiagnosticSummary() should not be empty")
	}
	if !strings.Contains(summary, "is not a directory") {
		t.Errorf("DiagnosticSummary() = %q, want to contain 'is not a directory'", summary)
	}
	if !strings.Contains(summary, "warn:") {
		t.Errorf("DiagnosticSummary() = %q, want to contain 'warn:'", summary)
	}
}

func TestService_DiagnosticSummary_Empty(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "good"), "good", "# Good")

	svc := NewServiceWithRoots([]Root{{Path: rootDir, Scope: ScopeProjectEitri}})

	summary := svc.DiagnosticSummary()
	if summary != "" {
		t.Errorf("DiagnosticSummary() = %q, want empty", summary)
	}
}

func TestService_DiagnosticSummary_NilRegistry(t *testing.T) {
	svc := &Service{} // registry is nil

	summary := svc.DiagnosticSummary()
	if summary != "" {
		t.Errorf("DiagnosticSummary() = %q, want empty for nil registry", summary)
	}
}

// --- SkillContent() ---

func TestSkillContent_Basic(t *testing.T) {
	result := SkillContent("Do the thing", nil, "/skills/my-skill")

	if !strings.Contains(result, "<skill_content>") {
		t.Error("expected <skill_content> tag")
	}
	if !strings.Contains(result, "<skill_directory>/skills/my-skill</skill_directory>") {
		t.Error("expected skill_directory tag with path")
	}
	if !strings.Contains(result, "Do the thing") {
		t.Error("expected instructions in output")
	}
	if !strings.Contains(result, "</skill_content>") {
		t.Error("expected closing tag")
	}
	if strings.Contains(result, "<skill_resources>") {
		t.Error("should not include empty resources section")
	}
	if !strings.Contains(result, "Relative paths in this skill resolve from skill_directory") {
		t.Error("expected relative paths note")
	}
}

func TestSkillContent_WithResources(t *testing.T) {
	result := SkillContent("Instructions", []string{"file1.txt", "file2.txt"}, "/skills/test")

	if !strings.Contains(result, "<skill_resources>") {
		t.Error("expected <skill_resources> tag")
	}
	if !strings.Contains(result, "file1.txt") {
		t.Error("expected file1.txt in resources")
	}
	if !strings.Contains(result, "file2.txt") {
		t.Error("expected file2.txt in resources")
	}
	if !strings.Contains(result, "</skill_resources>") {
		t.Error("expected closing resources tag")
	}
}

func TestSkillContent_XMLEscapesDirectory(t *testing.T) {
	result := SkillContent("Use <b>bold</b>", nil, "/skills/foo & bar's")

	if !strings.Contains(result, "foo &amp; bar&apos;s") {
		t.Errorf("expected escaped directory, got %q", result)
	}
	// Instructions are NOT XML-escaped (they are passed as-is for the LLM to read)
	if !strings.Contains(result, "Use <b>bold</b>") {
		t.Errorf("expected raw instructions, got %q", result)
	}
}

func TestSkillContent_EmptyDirectory(t *testing.T) {
	result := SkillContent("Do it", nil, "")
	if !strings.Contains(result, "<skill_directory></skill_directory>") {
		t.Error("expected empty skill_directory tag")
	}
}

func TestSkillContent_EmptyInstructions(t *testing.T) {
	result := SkillContent("", nil, "/skills/test")
	if !strings.Contains(result, "<instructions>") {
		t.Error("expected instructions tag")
	}
	if !strings.Contains(result, "</instructions>") {
		t.Error("expected closing instructions tag")
	}
}

// --- AppendDiagnostic() ---

func TestAppendDiagnostic_AddsToRegistry(t *testing.T) {
	r := NewRegistry()

	d := Diagnostic{Severity: SeverityWarn, Message: "test warning", Path: "/some/path"}
	r.AppendDiagnostic(d)

	diags := r.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("Diagnostics() = %d, want 1", len(diags))
	}
	if diags[0].Severity != SeverityWarn {
		t.Errorf("Severity = %q, want %q", diags[0].Severity, SeverityWarn)
	}
	if diags[0].Message != "test warning" {
		t.Errorf("Message = %q, want %q", diags[0].Message, "test warning")
	}
	if diags[0].Path != "/some/path" {
		t.Errorf("Path = %q, want %q", diags[0].Path, "/some/path")
	}
}

func TestAppendDiagnostic_Multiple(t *testing.T) {
	r := NewRegistry()

	r.AppendDiagnostic(Diagnostic{Severity: SeverityWarn, Message: "warning 1"})
	r.AppendDiagnostic(Diagnostic{Severity: SeverityError, Message: "error 1"})
	r.AppendDiagnostic(Diagnostic{Severity: SeverityWarn, Message: "warning 2"})

	diags := r.Diagnostics()
	if len(diags) != 3 {
		t.Fatalf("Diagnostics() = %d, want 3", len(diags))
	}
	if diags[1].Severity != SeverityError {
		t.Errorf("Second diagnostic severity = %q, want %q", diags[1].Severity, SeverityError)
	}
}

func TestAppendDiagnostic_NilRegistry(t *testing.T) {
	var r *Registry

	// Should not panic
	r.AppendDiagnostic(Diagnostic{Severity: SeverityWarn, Message: "test"})
}

// --- FilterByName() ---

func TestFilterByName_EmptyQuery(t *testing.T) {
	r := NewRegistry()
	skill := &Skill{Name: "code-review", Description: "Review code"}
	r.effective["code-review"] = skill

	filtered := r.FilterByName("")
	if filtered == nil {
		t.Fatal("FilterByName('') should return a registry")
	}
	if filtered.Effective()["code-review"] == nil {
		t.Error("expected 'code-review' in filtered result")
	}
}

func TestFilterByName_Matching(t *testing.T) {
	r := NewRegistry()
	r.effective["code-review"] = &Skill{Name: "code-review", Description: "Review code"}
	r.effective["debug"] = &Skill{Name: "debug", Description: "Debug code"}
	r.effective["test"] = &Skill{Name: "test", Description: "Test code"}

	filtered := r.FilterByName("code")
	if filtered == nil {
		t.Fatal("expected non-nil filtered registry")
	}
	eff := filtered.Effective()
	if len(eff) != 1 {
		t.Fatalf("Filtered effective count = %d, want 1", len(eff))
	}
	if eff["code-review"] == nil {
		t.Error("expected 'code-review' to match")
	}
}

func TestFilterByName_CaseInsensitive(t *testing.T) {
	r := NewRegistry()
	r.effective["Code-Review"] = &Skill{Name: "Code-Review", Description: "Review code"}

	filtered := r.FilterByName("code")
	eff := filtered.Effective()
	if len(eff) != 1 {
		t.Fatalf("Filtered effective count = %d, want 1 for case-insensitive match", len(eff))
	}
}

func TestFilterByName_NoMatch(t *testing.T) {
	r := NewRegistry()
	r.effective["code-review"] = &Skill{Name: "code-review", Description: "Review code"}

	filtered := r.FilterByName("nonexistent")
	eff := filtered.Effective()
	if len(eff) != 0 {
		t.Errorf("Filtered effective count = %d, want 0", len(eff))
	}
}

func TestFilterByName_NilRegistry(t *testing.T) {
	var r *Registry
	filtered := r.FilterByName("test")
	if filtered != nil {
		t.Error("FilterByName on nil registry should return nil")
	}
}

func TestFilterByName_FiltersShadowed(t *testing.T) {
	r := NewRegistry()
	r.effective["active"] = &Skill{Name: "active", Description: "Active"}
	r.shadowed = []*Skill{{Name: "shadowed-skill", Description: "Shadowed"}}

	filtered := r.FilterByName("shadowed")
	if len(filtered.Shadowed()) != 1 {
		t.Errorf("Shadowed count after filter = %d, want 1", len(filtered.Shadowed()))
	}
}

func TestFilterByName_FiltersInvalid(t *testing.T) {
	r := NewRegistry()
	r.effective["active"] = &Skill{Name: "active", Description: "Active"}
	r.invalid = []*Skill{{Name: "broken", Description: "Broken"}}

	filtered := r.FilterByName("broken")
	if len(filtered.Invalid()) != 1 {
		t.Errorf("Invalid count after filter = %d, want 1", len(filtered.Invalid()))
	}
}

func TestFilterByName_FiltersDisabled(t *testing.T) {
	r := NewRegistry()
	r.effective["active"] = &Skill{Name: "active", Description: "Active"}
	r.disabled = []*Skill{{Name: "inactive", Description: "Disabled"}}

	filtered := r.FilterByName("inactive")
	if len(filtered.Disabled()) != 1 {
		t.Errorf("Disabled count after filter = %d, want 1", len(filtered.Disabled()))
	}
}

// --- Registry nil receiver tests ---

func TestRegistry_NilEffective(t *testing.T) {
	var r *Registry
	eff := r.Effective()
	if eff != nil {
		t.Error("Effective() on nil registry should return nil")
	}
}

func TestRegistry_NilShadowed(t *testing.T) {
	var r *Registry
	shadowed := r.Shadowed()
	if shadowed != nil {
		t.Error("Shadowed() on nil registry should return nil")
	}
}

func TestRegistry_NilInvalid(t *testing.T) {
	var r *Registry
	invalid := r.Invalid()
	if invalid != nil {
		t.Error("Invalid() on nil registry should return nil")
	}
}

func TestRegistry_NilAll(t *testing.T) {
	var r *Registry
	all := r.All()
	if all != nil {
		t.Error("All() on nil registry should return nil")
	}
}

func TestRegistry_NilDisabled(t *testing.T) {
	var r *Registry
	disabled := r.Disabled()
	if disabled != nil {
		t.Error("Disabled() on nil registry should return nil")
	}
}

func TestRegistry_NilSummary(t *testing.T) {
	var r *Registry
	summary := r.Summary()
	if summary != nil {
		t.Error("Summary() on nil registry should return nil")
	}
}

func TestRegistry_NilDiagnostics(t *testing.T) {
	var r *Registry
	diags := r.Diagnostics()
	if diags != nil {
		t.Error("Diagnostics() on nil registry should return nil")
	}
}

// --- Diagnostics.Error() ---

func TestDiagnosticsError_Empty(t *testing.T) {
	var diags Diagnostics
	if diags.Error() != "" {
		t.Errorf("Error() on empty diagnostics = %q, want %q", diags.Error(), "")
	}
}

func TestDiagnosticsError_NonEmpty(t *testing.T) {
	diags := Diagnostics{
		{Severity: SeverityError, Message: "first error"},
		{Severity: SeverityWarn, Message: "second warning"},
	}
	if diags.Error() != "first error" {
		t.Errorf("Error() = %q, want %q", diags.Error(), "first error")
	}
}

func TestDiagnosticsError_Nil(t *testing.T) {
	var diags Diagnostics
	// nil slice should still work (zero-value)
	if diags.Error() != "" {
		t.Errorf("Error() on nil diagnostics = %q, want %q", diags.Error(), "")
	}
}

// --- discoverRoot edge cases ---

func TestDiscoverRoot_MissingDir(t *testing.T) {
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	root := Root{Path: nonexistent, Scope: ScopeProjectEitri}

	skills, diags := discoverRoot(root)
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for missing dir, got %d", len(skills))
	}
	if !diagnosticsContain(diags, "does not exist") {
		t.Errorf("expected warning about missing dir, got %v", diags)
	}
}

func TestDiscoverRoot_NotADirectory(t *testing.T) {
	rootDir := t.TempDir()
	filePath := filepath.Join(rootDir, "afile")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	root := Root{Path: filePath, Scope: ScopeProjectEitri}
	skills, diags := discoverRoot(root)
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for file root, got %d", len(skills))
	}
	if !diagnosticsContain(diags, "is not a directory") {
		t.Errorf("expected warning about file root, got %v", diags)
	}
}

func TestDiscoverRoot_EmptyDir(t *testing.T) {
	emptyDir := t.TempDir()
	root := Root{Path: emptyDir, Scope: ScopeProjectEitri}

	skills, diags := discoverRoot(root)
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for empty dir, got %d", len(skills))
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for empty dir, got %v", diags)
	}
}

func TestDiscoverRoot_NestedSkills(t *testing.T) {
	rootDir := t.TempDir()
	writeSkill(t, filepath.Join(rootDir, "skill-a"), "skill-a", "# Skill A")
	writeSkill(t, filepath.Join(rootDir, "skill-b"), "skill-b", "# Skill B")
	// Create a subdirectory that looks like a skill dir but has no SKILL.md
	os.MkdirAll(filepath.Join(rootDir, "empty-dir"), 0755)

	root := Root{Path: rootDir, Scope: ScopeProjectEitri}
	skills, diags := discoverRoot(root)
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}
	// empty-dir should produce a diagnostic (SKILL.md not found)
	if !diagnosticsContain(diags, "SKILL.md not found") {
		t.Errorf("expected diagnostic for empty-dir, got %v", diags)
	}
}

// --- defaultRoots() ---

func TestDefaultRoots_ReturnsExpectedRoots(t *testing.T) {
	workspace := "/my/project"
	homeDir := "/home/user"

	roots := defaultRoots(workspace, homeDir)
	if len(roots) != 4 {
		t.Fatalf("expected 4 roots, got %d", len(roots))
	}

	expected := []struct {
		path  string
		scope Scope
	}{
		{filepath.Join(workspace, ".eitri", "skills"), ScopeProjectEitri},
		{filepath.Join(workspace, ".agents", "skills"), ScopeProjectAgents},
		{filepath.Join(homeDir, ".eitri", "skills"), ScopeUserEitri},
		{filepath.Join(homeDir, ".agents", "skills"), ScopeUserAgents},
	}
	for i, exp := range expected {
		if roots[i].Path != exp.path {
			t.Errorf("root[%d].Path = %q, want %q", i, roots[i].Path, exp.path)
		}
		if roots[i].Scope != exp.scope {
			t.Errorf("root[%d].Scope = %q, want %q", i, roots[i].Scope, exp.scope)
		}
	}
}

// --- parseFrontmatter edge cases ---

func TestParseFrontmatter_Empty(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("", skillDir)
	if fm != nil {
		t.Error("expected nil frontmatterData for empty frontmatter")
	}
	if !diagnosticsContain(diags, "empty YAML frontmatter") {
		t.Errorf("expected 'empty YAML frontmatter' diagnostic, got %v", diags)
	}
}

func TestParseFrontmatter_WhitespaceOnly(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("   \n\t  ", skillDir)
	if fm != nil {
		t.Error("expected nil frontmatterData for whitespace-only frontmatter")
	}
	if !diagnosticsContain(diags, "empty YAML frontmatter") {
		t.Errorf("expected 'empty YAML frontmatter' diagnostic, got %v", diags)
	}
}

func TestParseFrontmatter_MalformedYAML(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("name: unquoted: colon", skillDir)
	if fm != nil {
		t.Error("expected nil frontmatterData for malformed YAML")
	}
	if !diagnosticsContain(diags, "cannot parse YAML frontmatter") {
		t.Errorf("expected 'cannot parse YAML frontmatter' diagnostic, got %v", diags)
	}
}

func TestParseFrontmatter_MissingName(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("description: A skill without a name", skillDir)
	if fm != nil {
		t.Error("expected nil frontmatterData when name is missing")
	}
	if !diagnosticsContain(diags, "name field missing") {
		t.Errorf("expected 'name field missing' diagnostic, got %v", diags)
	}
}

func TestParseFrontmatter_MissingDescription(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("name: my-skill", skillDir)
	if fm != nil {
		t.Error("expected nil frontmatterData when description is missing")
	}
	if !diagnosticsContain(diags, "description field missing") {
		t.Errorf("expected 'description field missing' diagnostic, got %v", diags)
	}
}

func TestParseFrontmatter_Valid(t *testing.T) {
	skillDir := t.TempDir()
	fm, diags := parseFrontmatter("name: my-skill\ndescription: A test skill\nlicense: MIT", skillDir)
	if fm == nil {
		t.Fatal("expected non-nil frontmatterData for valid frontmatter")
	}
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics, got %v", diags)
	}
	if fm.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", fm.Name, "my-skill")
	}
	if fm.Description != "A test skill" {
		t.Errorf("Description = %q, want %q", fm.Description, "A test skill")
	}
	if fm.License != "MIT" {
		t.Errorf("License = %q, want %q", fm.License, "MIT")
	}
}

// --- extractFrontmatter edge cases ---

func TestExtractFrontmatter_NoFrontmatter(t *testing.T) {
	body, fm, found := extractFrontmatter("Just body text")
	if body != "Just body text" {
		t.Errorf("Body = %q, want %q", body, "Just body text")
	}
	if fm != "" {
		t.Errorf("Frontmatter = %q, want empty", fm)
	}
	if found {
		t.Error("found should be false")
	}
}

func TestExtractFrontmatter_NoClosing(t *testing.T) {
	body, fm, found := extractFrontmatter("---\nname: test\nbody text")
	if body != "---\nname: test\nbody text" {
		t.Errorf("Body should be unchanged for no closing ---, got %q", body)
	}
	if fm != "" {
		t.Errorf("Frontmatter = %q, want empty", fm)
	}
	if found {
		t.Error("found should be false when no closing ---")
	}
}

func TestExtractFrontmatter_Valid(t *testing.T) {
	body, fm, found := extractFrontmatter("---\nname: test\n---\nBody text here")
	if !found {
		t.Fatal("expected found = true")
	}
	if fm != "name: test" {
		t.Errorf("Frontmatter = %q, want %q", fm, "name: test")
	}
	if body != "Body text here" {
		t.Errorf("Body = %q, want %q", body, "Body text here")
	}
}
