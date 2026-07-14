package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

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

func (ctx sessionSkillContext) systemPromptNote() string {
	if len(ctx.Activations) == 0 && len(ctx.Warnings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Session-scoped active Skills for current Run are injected as activate_skill tool-call history before current user message. Use only those current-Run activate_skill results as authoritative Skill context. Ignore older activate_skill results from earlier turns because they may be stale or superseded.")
	if len(ctx.Activations) > 0 {
		b.WriteString("\n\nCurrent active Skills for this Run:\n")
		for _, activation := range ctx.Activations {
			b.WriteString("- ")
			b.WriteString(activation.Name)
			b.WriteString(" [context ")
			b.WriteString(skillContextFingerprint(activation.Content))
			b.WriteString("]\n")
		}
	}
	if len(ctx.Warnings) > 0 {
		b.WriteString("\n\nInactive Skills removed for this Run:\n")
		for _, warning := range ctx.Warnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func (rm *RunManager) resolveSessionSkillContext(sessionID string) sessionSkillContext {
	if rm == nil || rm.uiSessionMgr == nil || rm.skillsSvc == nil {
		return sessionSkillContext{}
	}

	names := rm.uiSessionMgr.ActiveSkills(sessionID)
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

		skill := rm.skillsSvc.Lookup(name)
		if skill == nil {
			rm.uiSessionMgr.DeactivateSkill(sessionID, name)
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

func skillContextFingerprint(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum[:6])
}

type skillContextLLM struct {
	base        model.LLM
	activations []runSkillActivation
}

func newSkillContextLLM(base model.LLM, activations []runSkillActivation) model.LLM {
	if len(activations) == 0 {
		return base
	}
	copied := append([]runSkillActivation(nil), activations...)
	return &skillContextLLM{base: base, activations: copied}
}

func (m *skillContextLLM) Name() string {
	return m.base.Name()
}

func (m *skillContextLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if len(m.activations) == 0 {
		return m.base.GenerateContent(ctx, req, stream)
	}

	cloned := *req
	cloned.Contents = append(skillActivationContents(m.activations), req.Contents...)
	return m.base.GenerateContent(ctx, &cloned, stream)
}

func skillActivationContents(activations []runSkillActivation) []*genai.Content {
	contents := make([]*genai.Content, 0, len(activations)*2)
	for i, activation := range activations {
		callID := fmt.Sprintf("active_skill_%d_%s", i, activation.Name)
		contents = append(contents,
			&genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{
						ID:   callID,
						Name: "activate_skill",
						Args: map[string]any{"name": activation.Name},
					},
				}},
			},
			&genai.Content{
				Role: genai.RoleUser,
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       callID,
						Name:     "activate_skill",
						Response: map[string]any{"content": activation.Content},
					},
				}},
			},
		)
	}
	return contents
}
