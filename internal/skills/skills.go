// Package skills implements Agent Skills discovery, parsing, registry management,
// and resource manifest generation.
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
	StatusDisabled  Status = "disabled"
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
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Body          string         `json:"body"`
	Path          string         `json:"path"`
	Scope         Scope          `json:"scope"`
	Status        Status         `json:"status"`
	License       string         `json:"license,omitempty"`
	Compatibility string         `json:"compatibility,omitempty"`
	AllowedTools  []string       `json:"allowed-tools,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
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
	effective   map[string]*Skill // name → effective skill (deduplicated by precedence)
	shadowed    []*Skill          // skills overridden by higher-precedence skills
	all         []*Skill          // all parsed skills including shadowed
	invalid     []*Skill          // skills that failed validation
	disabled    []*Skill          // skills explicitly disabled
	diagnostics Diagnostics       // discovery + registry diagnostics
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		effective: make(map[string]*Skill),
	}
}

// Effective returns the map of effective skills (name → skill).
// Disabled skills are excluded.
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

// Disabled returns skills that have been explicitly disabled.
func (r *Registry) Disabled() []*Skill {
	if r == nil {
		return nil
	}
	return r.disabled
}

// Summary returns lightweight summaries for effective skills (excluding disabled).
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
	result := make(Diagnostics, len(r.diagnostics))
	copy(result, r.diagnostics)
	return result
}

// AppendDiagnostic adds a diagnostic to the registry.
func (r *Registry) AppendDiagnostic(d Diagnostic) {
	if r == nil {
		return
	}
	r.diagnostics = append(r.diagnostics, d)
}

// FilterByName returns a new Registry with only skills whose name contains
// the query string (case-insensitive). Empty/missing query returns all skills.
func (r *Registry) FilterByName(q string) *Registry {
	if r == nil {
		return nil
	}
	if q == "" {
		return r
	}
	q = strings.ToLower(q)

	filtered := NewRegistry()
	filtered.diagnostics = r.diagnostics

	for name, skill := range r.effective {
		if strings.Contains(strings.ToLower(name), q) {
			if filtered.effective == nil {
				filtered.effective = make(map[string]*Skill)
			}
			filtered.effective[name] = skill
		}
	}

	for _, skill := range r.shadowed {
		if strings.Contains(strings.ToLower(skill.Name), q) {
			filtered.shadowed = append(filtered.shadowed, skill)
		}
	}

	for _, skill := range r.invalid {
		if strings.Contains(strings.ToLower(skill.Name), q) {
			filtered.invalid = append(filtered.invalid, skill)
		}
	}

	for _, skill := range r.disabled {
		if strings.Contains(strings.ToLower(skill.Name), q) {
			filtered.disabled = append(filtered.disabled, skill)
		}
	}

	return filtered
}

// BuildRegistry resolves precedence across all discovered skills and builds a Registry.
// Skills with the same name are resolved by root order (earlier = higher precedence).
// The disabled set specifies skill names to exclude from effective, summary, and other outputs.
func BuildRegistry(discovered []*Skill, discoveryDiags Diagnostics, disabled []string) *Registry {
	r := NewRegistry()
	r.diagnostics = append(r.diagnostics, discoveryDiags...)
	if len(discovered) == 0 {
		return r
	}

	// Build disabled name set for fast lookup
	disabledSet := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		disabledSet[name] = true
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
			r.diagnostics = append(r.diagnostics, Diagnostic{
				Severity: SeverityWarn,
				Message:  fmt.Sprintf("Skill %q shadowed by higher-precedence skill", skill.Name),
				Path:     skill.Path,
				Skill:    skill.Name,
			})

			// Keep the higher-precedence one in effective (if not disabled)
			if _, ok := r.effective[skill.Name]; !ok {
				if !disabledSet[skill.Name] {
					r.effective[skill.Name] = existing
				}
			}
		} else {
			seen[skill.Name] = findIndex(skill, discovered)
			if disabledSet[skill.Name] {
				skill.Status = StatusDisabled
				r.disabled = append(r.disabled, skill)
			} else {
				skill.Status = StatusEffective
				r.effective[skill.Name] = skill
			}
		}
	}

	// Remove any effective skills that are in the disabled set (for cases like shadowed
	// where the higher-precedence one might still be in effective even though it shouldn't be)
	for name := range disabledSet {
		delete(r.effective, name)
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
