package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	"github.com/glemsom/eitri/internal/skills"
)

// writeTestSkill writes a minimal SKILL.md to the given root directory.
func writeTestSkill(t *testing.T, rootDir, name, body string) {
	t.Helper()
	skillDir := filepath.Join(rootDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir %s: %v", skillDir, err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// newSkillsServiceWithSkill creates a skills service with a single test skill.
func newSkillsServiceWithSkill(t *testing.T, skillName, skillBody string) *skills.Service {
	t.Helper()
	rootDir := t.TempDir()
	writeTestSkill(t, rootDir, skillName, skillBody)
	return skills.NewServiceWithRoots([]skills.Root{
		{Path: rootDir, Scope: skills.ScopeProjectEitri},
	})
}

// newSkillsServiceWithSkills creates a skills service with multiple test skills.
func newSkillsServiceWithSkills(t *testing.T, skillMap map[string]string) *skills.Service {
	t.Helper()
	rootDir := t.TempDir()
	for name, body := range skillMap {
		writeTestSkill(t, rootDir, name, body)
	}
	return skills.NewServiceWithRoots([]skills.Root{
		{Path: rootDir, Scope: skills.ScopeProjectEitri},
	})
}

func TestBuildSystemPrompt_NoRequiredSkills(t *testing.T) {
	// When persona has no RequiredSkills, no required-skills directive should appear.
	workspace := t.TempDir()
	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:         "simple-agent",
		SystemPrompt: "You are a simple agent.",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "simple-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, nil)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(sysPrompt, "You are a simple agent.") {
		t.Fatalf("system prompt should contain persona's custom prompt, got:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "<required_skills>") {
		t.Fatalf("system prompt should NOT contain required skills directive when persona has none, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_SingleRequiredSkill(t *testing.T) {
	// A persona with a single required skill should produce a directive.
	workspace := t.TempDir()
	skillName := "code-review"
	skillBody := "# Code Review\n\nReview code for bugs and security issues."
	skillsSvc := newSkillsServiceWithSkill(t, skillName, skillBody)

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "reviewer",
		SystemPrompt:   "You are a code reviewer.",
		RequiredSkills: []string{skillName},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "reviewer",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Must contain the directive
	if !strings.Contains(sysPrompt, "Required skills for this persona: code-review") {
		t.Fatalf("system prompt should contain required skills directive for code-review, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, `On your first turn, call skill("name") for each`) {
		t.Fatalf("system prompt should contain call-to-action on first turn, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "<required_skills>") {
		t.Fatalf("system prompt should contain <required_skills> opening tag, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "</required_skills>") {
		t.Fatalf("system prompt should contain </required_skills> closing tag, got:\n%s", sysPrompt)
	}

	// Must NOT contain the skill body (no pre-injection)
	if strings.Contains(sysPrompt, skillBody) {
		t.Fatalf("system prompt should NOT contain skill body (pre-injection removed), got:\n%s", sysPrompt)
	}

	// Must contain the persona prompt
	if !strings.Contains(sysPrompt, "You are a code reviewer.") {
		t.Fatalf("system prompt should contain persona's custom prompt, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_MultipleRequiredSkills(t *testing.T) {
	// A persona with multiple required skills should list all of them.
	workspace := t.TempDir()
	skillsMap := map[string]string{
		"code-review":   "# Code Review\n\nReview code.",
		"security-scan": "# Security Scan\n\nScan for vulnerabilities.",
		"test-writer":   "# Test Writer\n\nWrite tests.",
	}
	skillsSvc := newSkillsServiceWithSkills(t, skillsMap)

	skillNames := []string{"code-review", "security-scan", "test-writer"}
	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "multi-skill-agent",
		SystemPrompt:   "You are a multi-skill agent.",
		RequiredSkills: skillNames,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "multi-skill-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Must contain all skill names in the directive
	if !strings.Contains(sysPrompt, "Required skills for this persona: code-review, security-scan, test-writer") {
		t.Fatalf("system prompt should list all required skills, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, `<required_skills>`) {
		t.Fatalf("system prompt should contain <required_skills> block, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, `On your first turn, call skill("name") for each`) {
		t.Fatalf("system prompt should contain call-to-action on first turn, got:\n%s", sysPrompt)
	}

	// Must NOT contain any skill bodies
	for _, body := range skillsMap {
		if strings.Contains(sysPrompt, body) {
			t.Fatalf("system prompt should NOT contain any skill body (pre-injection removed), got:\n%s", sysPrompt)
		}
	}

	// Must contain the persona prompt
	if !strings.Contains(sysPrompt, "You are a multi-skill agent.") {
		t.Fatalf("system prompt should contain persona's custom prompt, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_RequiredSkillNotFound(t *testing.T) {
	// When a persona references a required skill that doesn't exist in the skills service,
	// it should be gracefully skipped with a warning log (not crash, not included in directive).
	workspace := t.TempDir()
	skillsSvc := newSkillsServiceWithSkill(t, "existing-skill", "# Existing\n\nDoes exist.")

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "missing-skill-agent",
		SystemPrompt:   "You are an agent.",
		RequiredSkills: []string{"nonexistent-skill", "existing-skill"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "missing-skill-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Only the existing skill should appear in the directive
	if strings.Contains(sysPrompt, "nonexistent-skill") {
		t.Fatalf("system prompt should NOT contain nonexistent skill name, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "existing-skill") {
		t.Fatalf("system prompt should contain existing skill name, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "Required skills for this persona: existing-skill") {
		t.Fatalf("system prompt should only list existing skill, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_MixedActivations(t *testing.T) {
	// Both manually activated skills (via skill tool) and persona-required skills
	// should coexist: manual skills get content injected, required skills get directive.
	workspace := t.TempDir()
	requiredSkillName := "required-one"
	manualSkillName := "manual-one"
	requiredBody := "# Required\n\nRequired skill body."
	manualBody := "# Manual\n\nManual skill body."

	skillsSvc := newSkillsServiceWithSkills(t, map[string]string{
		requiredSkillName: requiredBody,
		manualSkillName:   manualBody,
	})

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "mixed-agent",
		SystemPrompt:   "You are a mixed agent.",
		RequiredSkills: []string{requiredSkillName},
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate a manual activation that already happened via the skill() tool.
	skillCtx := sessionSkillContext{
		Activations: []runSkillActivation{
			{
				Name:    manualSkillName,
				Content: skills.SkillContent("# Manual\n\nManual skill body.", nil, ""),
			},
		},
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "mixed-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, skillCtx, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Must contain the required skills directive for the persona skill
	if !strings.Contains(sysPrompt, "Required skills for this persona: required-one") {
		t.Fatalf("system prompt should contain required skills directive, got:\n%s", sysPrompt)
	}

	// Must contain the manually activated skill content
	if !strings.Contains(sysPrompt, `Activated skill "manual-one":`) {
		t.Fatalf("system prompt should contain activated skill label for manual skill, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, manualBody) {
		t.Fatalf("system prompt should contain manually activated skill body, got:\n%s", sysPrompt)
	}

	// Must NOT contain the required skill body
	if strings.Contains(sysPrompt, requiredBody) {
		t.Fatalf("system prompt should NOT contain required skill body (pre-injection removed), got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_DefaultPromptWithRequiredSkills(t *testing.T) {
	// When a persona has required skills but no system prompt override,
	// use the default prompt plus the directive.
	workspace := t.TempDir()
	skillName := "my-skill"
	skillsSvc := newSkillsServiceWithSkill(t, skillName, "# My Skill\n\nContent.")

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "default-prompt-agent",
		RequiredSkills: []string{skillName},
		// No SystemPrompt set — should use the package-level DefaultPrompt
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "default-prompt-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Must contain the built-in default prompt
	if !strings.Contains(sysPrompt, "You are Eitri, an expert AI coding agent.") {
		t.Fatalf("system prompt should contain built-in default prompt, got:\n%s", sysPrompt)
	}

	// Must contain the required skills directive
	if !strings.Contains(sysPrompt, "Required skills for this persona: my-skill") {
		t.Fatalf("system prompt should contain required skills directive, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_SkillsCatalogPresent(t *testing.T) {
	// The "Available skills" catalog should still be present alongside the directive.
	workspace := t.TempDir()
	reqSkill := "required-skill"
	otherSkill := "other-skill"
	skillsSvc := newSkillsServiceWithSkills(t, map[string]string{
		reqSkill:   "# Required\n\nRequired skill.",
		otherSkill: "# Other\n\nOther available skill.",
	})

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "catalog-agent",
		SystemPrompt:   "You are a catalog agent.",
		RequiredSkills: []string{reqSkill},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:     workspace,
		ActivePersona: "catalog-agent",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// The available skills catalog should still be present
	if !strings.Contains(sysPrompt, "Available skills:") {
		t.Fatalf("system prompt should contain skills catalog, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, otherSkill) {
		t.Fatalf("system prompt should contain other available skills in the catalog, got:\n%s", sysPrompt)
	}

	// The required skills directive should also be present
	if !strings.Contains(sysPrompt, "Required skills for this persona: required-skill") {
		t.Fatalf("system prompt should contain required skills directive, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_UserOverrideSkipsDirective(t *testing.T) {
	// When the user provides a system prompt override (cfg.SystemPrompt != ""),
	// persona-injected skills are not loaded at all, so no directive should appear.
	workspace := t.TempDir()
	skillsSvc := newSkillsServiceWithSkill(t, "some-skill", "# Some Skill\n\nContent.")

	if err := persona.Save(workspace, &persona.PersonaDefinition{
		Name:           "ignored-persona",
		SystemPrompt:   "You are an ignored persona.",
		RequiredSkills: []string{"some-skill"},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := runconfig.RunConfig{
		Workspace:      workspace,
		ActivePersona:  "ignored-persona",
		SystemPrompt:   "You are the user override prompt.",
	}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, skillsSvc)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	// Should use the user override, not the persona prompt
	if !strings.Contains(sysPrompt, "You are the user override prompt.") {
		t.Fatalf("system prompt should contain user override, got:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "You are an ignored persona.") {
		t.Fatalf("system prompt should NOT contain persona prompt when override given, got:\n%s", sysPrompt)
	}

	// No required skills directive since persona was bypassed
	if strings.Contains(sysPrompt, "Required skills for this persona:") {
		t.Fatalf("system prompt should NOT contain required skills directive with user override, got:\n%s", sysPrompt)
	}
}

func TestBuildSystemPrompt_EmptyActivationsNoPersona(t *testing.T) {
	// With no persona and no activations, should produce just the default prompt
	// plus repo instructions (if any). No directive, no catalog.
	cfg := runconfig.RunConfig{}
	sysPrompt, err := buildSystemPrompt(cfg, sessionSkillContext{}, nil)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}

	if !strings.Contains(sysPrompt, "You are Eitri, an expert AI coding agent.") {
		t.Fatalf("system prompt should contain default prompt, got:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "Required skills") {
		t.Fatalf("system prompt should NOT contain required skills directive with no persona, got:\n%s", sysPrompt)
	}
}
