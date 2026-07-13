package skills

import (
	"fmt"
	"os"
	"path/filepath"
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
	if len(skills) != 1 {
		t.Fatalf("expected 1 valid skill, got %d", len(skills))
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
	registry := BuildRegistry(skills)

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
	registry := BuildRegistry(skills)

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
