package runner

import (
	"fmt"

	"github.com/glemsom/eitri/internal/skills"
)

type runSkillActivation struct {
	Name    string
	Content string
}

type sessionSkillContext struct {
	Activations []runSkillActivation
	Warnings    []string
}

func (s *RunService) resolveSessionSkillContext(sessionID string) sessionSkillContext {
	if s == nil || s.uiSessionMgr == nil || s.skillsSvc == nil {
		return sessionSkillContext{}
	}

	names := s.uiSessionMgr.ActiveSkills(sessionID)
	if len(names) == 0 {
		return sessionSkillContext{}
	}

	resolved := sessionSkillContext{Activations: make([]runSkillActivation, 0, len(names))}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		skill := s.skillsSvc.Lookup(name)
		if skill == nil {
			s.uiSessionMgr.DeactivateSkill(sessionID, name)
			resolved.Warnings = append(resolved.Warnings, staleSkillWarning(name))
			continue
		}

		resources := skills.ListResources(skill.Path)
		resolved.Activations = append(resolved.Activations, runSkillActivation{
			Name:    skill.Name,
			Content: skills.SkillContent(skill.Body, resources, skill.Path),
		})
	}
	return resolved
}

func staleSkillWarning(name string) string {
	return fmt.Sprintf("Active Skill %q no longer available. Skipped for this Run and removed from active Skills.", name)
}

func (s *RunService) skillDirectories() []string {
	if s.skillsSvc == nil {
		return nil
	}
	return s.skillsSvc.SkillDirectories()
}
