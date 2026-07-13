// Package skills implements Agent Skills discovery, parsing, registry management,
// and resource manifest generation as specified in SPEC §4.4.
package skills

import (
	"fmt"
	"strings"
)

// Scope defines the origin scope of a skill root.
type Scope string

const (
	ScopeUnknown       Scope = ""
	ScopeProjectEitri  Scope = "project-eitri"
	ScopeProjectAgents Scope = "project-agents"
	ScopeUserEitri     Scope = "user-eitri"
	ScopeUserAgents    Scope = "user-agents"
)

// Status represents the loading status of a skill.
type Status string

const (
	StatusEffective Status = "effective"
	StatusShadowed  Status = "shadowed"
	StatusInvalid   Status = "invalid"
)

// Severity for diagnostics.
type Severity string

const (
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// Diagnostic represents a validation or discovery issue.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Path     string   `json:"path,omitempty"`
	Skill    string   `json:"skill,omitempty"`
}

// Diagnostics is a slice of Diagnostic.
type Diagnostics []Diagnostic

func (d Diagnostics) Error() string {
	if len(d) == 0 {
		return ""
	}
	return d[0].Message
}

// HasSeverity returns true if any diagnostic has the given severity.
func HasSeverity(diags Diagnostics, sev Severity) bool {
	for _, d := range diags {
		if d.Severity == sev {
			return true
		}
	}
	return false
}

// Root represents a fixed skill discovery root.
type Root struct {
	Path  string `json:"path"`
	Scope Scope  `json:"scope"`
}

// Skill represents a parsed Agent Skill.
type Skill struct {
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Body          string                 `json:"body"`
	Path          string                 `json:"path"`
	Scope         Scope                  `json:"scope"`
	Status        Status                 `json:"status"`
	License       string                 `json:"license,omitempty"`
	Compatibility string                 `json:"compatibility,omitempty"`
	AllowedTools  []string               `json:"allowed-tools,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// SkillSummary is a lightweight representation for the skills catalog in system prompts.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       Scope  `json:"scope"`
}

// ActivatedSkill represents a skill activated for a session.
type ActivatedSkill struct {
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Resources    []string `json:"resources"`
	Directory    string   `json:"directory"`
}

// xmlEscape escapes special XML characters in a string.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// SkillContent formats skill activation content as XML for the model prompt.
func SkillContent(instructions string, resources []string, directory string) string {
	content := "<skill_content>\n"
	content += "<skill_directory>" + xmlEscape(directory) + "</skill_directory>\n"
	content += "\n<instructions>\n" + instructions + "\n</instructions>\n"
	if len(resources) > 0 {
		content += "\n<skill_resources>\n"
		for _, r := range resources {
			content += "  <file>" + xmlEscape(r) + "</file>\n"
		}
		content += "</skill_resources>\n"
	}
	content += "\nRelative paths in this skill resolve from skill_directory.\n"
	content += "Use file_viewer for references/assets. Scripts are not executed automatically.\n"
	content += "</skill_content>"
	return content
}

// Registry holds the resolved skill state.
type Registry struct {
	effective map[string]*Skill // name → effective skill (deduplicated by precedence)
	shadowed  []*Skill          // skills overridden by higher-precedence skills
	all       []*Skill          // all parsed skills including shadowed
	invalid   []*Skill          // skills that failed validation
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		effective: make(map[string]*Skill),
	}
}

// Effective returns the map of effective skills (name → skill).
func (r *Registry) Effective() map[string]*Skill {
	if r == nil {
		return nil
	}
	return r.effective
}

// Shadowed returns skills that are shadowed by higher-precedence skills.
func (r *Registry) Shadowed() []*Skill {
	if r == nil {
		return nil
	}
	return r.shadowed
}

// Invalid returns skills with non-recoverable parse errors.
func (r *Registry) Invalid() []*Skill {
	if r == nil {
		return nil
	}
	return r.invalid
}

// All returns all parsed skills including shadowed and invalid.
func (r *Registry) All() []*Skill {
	if r == nil {
		return nil
	}
	return r.all
}

// Summary returns lightweight summaries for effective skills.
func (r *Registry) Summary() []SkillSummary {
	if r == nil {
		return nil
	}
	su := make([]SkillSummary, 0, len(r.effective))
	for _, s := range r.effective {
		su = append(su, SkillSummary{
			Name:        s.Name,
			Description: s.Description,
			Scope:       s.Scope,
		})
	}
	return su
}

// Diagnostics returns diagnostics for the registry state.
func (r *Registry) Diagnostics() Diagnostics {
	if r == nil {
		return nil
	}
	var diags Diagnostics
	for _, s := range r.shadowed {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Skill %q shadowed by higher-precedence skill", s.Name),
			Path:     s.Path,
			Skill:    s.Name,
		})
	}
	for _, s := range r.invalid {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("Skill %q is invalid", s.Name),
			Path:     s.Path,
			Skill:    s.Name,
		})
	}
	return diags
}

// BuildRegistry resolves precedence across all discovered skills and builds a Registry.
// Skills with the same name are resolved by root order (earlier = higher precedence).
func BuildRegistry(discovered []*Skill) *Registry {
	r := NewRegistry()
	if len(discovered) == 0 {
		return r
	}

	// Track seen names for dedup
	seen := make(map[string]int) // name → index in discovered

	for _, skill := range discovered {
		r.all = append(r.all, skill)

		if skill.Status == StatusInvalid {
			r.invalid = append(r.invalid, skill)
			continue
		}

		if firstIdx, exists := seen[skill.Name]; exists {
			// Already have a higher-precedence skill with this name
			existing := discovered[firstIdx]
			// Mark lower-precedence (this one) as shadowed
			skill.Status = StatusShadowed
			r.shadowed = append(r.shadowed, skill)

			// Keep the higher-precedence one in effective
			if _, ok := r.effective[skill.Name]; !ok {
				r.effective[skill.Name] = existing
			}
		} else {
			seen[skill.Name] = findIndex(skill, discovered)
			skill.Status = StatusEffective
			r.effective[skill.Name] = skill
		}
	}

	return r
}

// findIndex returns the index of skill in the discovered slice.
func findIndex(skill *Skill, discovered []*Skill) int {
	for i, s := range discovered {
		if s == skill {
			return i
		}
	}
	return -1
}
