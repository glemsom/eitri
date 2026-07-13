package skills

import (
	"os"
	"strings"
	"sync"
)

// Service manages Agent Skills discovery, registry, and lookup.
type Service struct {
	mu     sync.RWMutex
	roots  []Root
	home   string
	workspace string
	registry *Registry
}

// NewService creates a Service with default discovery roots.
func NewService() *Service {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	s := &Service{
		roots:  defaultRoots(cwd, home),
		home:   home,
		workspace: cwd,
	}
	// Initial scan
	s.Refresh()
	return s
}

// NewServiceWithRoots creates a Service with explicit roots (for testing).
func NewServiceWithRoots(roots []Root) *Service {
	s := &Service{
		roots: roots,
	}
	s.Refresh()
	return s
}

// Roots returns the configured discovery roots.
func (s *Service) Roots() []Root {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Root, len(s.roots))
	copy(result, s.roots)
	return result
}

// SetRoots replaces the discovery roots (for testing).
func (s *Service) SetRoots(roots []Root) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots = roots
}

// Discover scans all roots and returns discovered skills and diagnostics.
func (s *Service) Discover() ([]*Skill, Diagnostics) {
	s.mu.RLock()
	roots := s.roots
	s.mu.RUnlock()
	return DiscoverSkills(roots)
}

// Refresh rescans all roots and rebuilds the registry.
// Returns the updated registry.
func (s *Service) Refresh() *Registry {
	skills, _ := s.Discover()
	registry := BuildRegistry(skills)

	s.mu.Lock()
	s.registry = registry
	s.mu.Unlock()

	return registry
}

// Registry returns the current registry.
func (s *Service) Registry() *Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registry
}

// Effective returns the map of effective skills (name → skill).
func (s *Service) Effective() map[string]*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	return s.registry.Effective()
}

// Shadowed returns skills shadowed by higher-precedence skills.
func (s *Service) Shadowed() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	return s.registry.Shadowed()
}

// Invalid returns skills with non-recoverable parse errors.
func (s *Service) Invalid() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	return s.registry.Invalid()
}

// All returns all parsed skills.
func (s *Service) All() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	return s.registry.All()
}

// Lookup returns an effective skill by name. Returns nil if not found.
// Lookup is case-insensitive for user convenience.
func (s *Service) Lookup(name string) *Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}

	// Exact lookup first
	if skill, ok := s.registry.effective[name]; ok {
		return skill
	}

	// Case-insensitive fallback
	lower := strings.ToLower(name)
	for _, skill := range s.registry.effective {
		if strings.ToLower(skill.Name) == lower {
			return skill
		}
	}

	return nil
}

// SkillsCatalogXML returns an XML fragment listing effective skills for the system prompt.
// Returns empty string if no effective skills exist.
func (s *Service) SkillsCatalogXML() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil || len(s.registry.effective) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<available_skills>\n")
	for _, skill := range s.registry.effective {
		b.WriteString("  <skill>\n")
		b.WriteString("    <name>" + xmlEscape(skill.Name) + "</name>\n")
		b.WriteString("    <description>" + xmlEscape(skill.Description) + "</description>\n")
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// SkillDirectories returns the list of skill directory paths effective for
// file_viewer path validation.
func (s *Service) SkillDirectories() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	var dirs []string
	for _, skill := range s.registry.effective {
		dirs = append(dirs, skill.Path)
	}
	return dirs
}

// HomeDir returns the user's home directory.
func (s *Service) HomeDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.home
}

// Workspace returns the workspace directory.
func (s *Service) Workspace() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workspace
}

// DiagnosticSummary returns a formatted string of all registry diagnostics.
func (s *Service) DiagnosticSummary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return ""
	}
	diags := s.registry.Diagnostics()
	if len(diags) == 0 {
		return ""
	}
	var msg string
	for _, d := range diags {
		msg += string(d.Severity) + ": " + d.Message
		if d.Path != "" {
			msg += " (" + d.Path + ")"
		}
		msg += "\n"
	}
	return msg
}


