package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// DiscoverSkills scans the given root directories for Agent Skills.
// For each root, it looks for subdirectories containing a SKILL.md file.
// Missing roots are skipped with a warning; unreadable roots produce diagnostics
// but do not block the overall discovery.
func DiscoverSkills(roots []Root) ([]*Skill, Diagnostics) {
	var skills []*Skill
	var diags Diagnostics

	for _, root := range roots {
		rootSkills, rootDiags := discoverRoot(root)
		diags = append(diags, rootDiags...)
		skills = append(skills, rootSkills...)
	}

	return skills, diags
}

// discoverRoot scans a single root directory for skills.
func discoverRoot(root Root) ([]*Skill, Diagnostics) {
	var skills []*Skill
	var diags Diagnostics

	info, err := os.Stat(root.Path)
	if err != nil {
		if os.IsNotExist(err) {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarn,
				Message:  fmt.Sprintf("Skill root %q does not exist, skipping", root.Path),
			})
			return skills, diags
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Cannot read skill root %q: %v, skipping", root.Path, err),
		})
		return skills, diags
	}

	if !info.IsDir() {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Skill root %q is not a directory, skipping", root.Path),
		})
		return skills, diags
	}

	entries, err := os.ReadDir(root.Path)
	if err != nil {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Cannot read skill root %q: %v", root.Path, err),
		})
		return skills, diags
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(root.Path, entry.Name())
		skill, skillDiags := ParseSKILLMD(skillDir)
		if skill != nil {
			skill.Scope = root.Scope
			skills = append(skills, skill)
		}
		diags = append(diags, skillDiags...)
	}

	return skills, diags
}

// defaultRoots returns the default skill discovery roots with scope assignment.
// Roots are in precedence order (highest first).
func defaultRoots(workspace, homeDir string) []Root {
	return []Root{
		{Path: filepath.Join(workspace, ".eitri", "skills"), Scope: ScopeProjectEitri},
		{Path: filepath.Join(workspace, ".agents", "skills"), Scope: ScopeProjectAgents},
		{Path: filepath.Join(homeDir, ".eitri", "skills"), Scope: ScopeUserEitri},
		{Path: filepath.Join(homeDir, ".agents", "skills"), Scope: ScopeUserAgents},
	}
}

// ScopeOrder maps scope to precedence level (lower = higher precedence).
var ScopeOrder = map[Scope]int{
	ScopeProjectEitri:  1,
	ScopeProjectAgents: 2,
	ScopeUserEitri:     3,
	ScopeUserAgents:    4,
}
