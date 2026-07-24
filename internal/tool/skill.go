package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

type skillArgs struct {
	Name string `json:"name" jsonschema:"Name of the skill to activate"`
}

// SkillTool implements ToolHandler for activating skills.
type SkillTool struct {
	skillsSvc    *skills.Service
	uiSessionMgr *session.Manager
	schema       litellm.Schema
}

// NewSkill creates a new SkillTool.
func NewSkill(skillsSvc *skills.Service, uiSessionMgr *session.Manager) *SkillTool {
	return &SkillTool{
		skillsSvc:    skillsSvc,
		uiSessionMgr: uiSessionMgr,
		schema:       SchemaOf[skillArgs](),
	}
}

func (t *SkillTool) Name() string {
	return "skill"
}

func (t *SkillTool) Description() string {
	return "Activate a skill by name. Skills provide reusable instructions, references, and scripts for specialized tasks. Returns skill content with instructions and resources."
}

func (t *SkillTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *SkillTool) Call(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var parsed skillArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ToolResult{}, fmt.Errorf("skill: invalid args: %w", err)
	}

	if parsed.Name == "" {
		return ToolError(TextBlocks("Error: skill name is required")), nil
	}

	if t.skillsSvc == nil {
		return ToolError(TextBlocks("Error: skills service not available")), nil
	}

	skill := t.skillsSvc.Lookup(parsed.Name)
	if skill == nil {
		// Check if disabled
		if t.skillsSvc.IsDisabled(parsed.Name) {
			return ToolError(TextBlocks(fmt.Sprintf("Error: skill %q is disabled. Enable it from the Skills page.", parsed.Name))), nil
		}
		return ToolError(TextBlocks(fmt.Sprintf("Error: skill %q not found in effective skills", parsed.Name))), nil
	}

	// Record activation in session for persistence across runs
	if t.uiSessionMgr != nil {
		sessionID, _ := ctx.Value(SessionIDKey).(string)
		if sessionID != "" {
			t.uiSessionMgr.ActivateSkill(sessionID, parsed.Name)
		}
	}

	resources := skills.ListResources(skill.Path)
	content := skills.SkillContent(skill.Body, resources, skill.Path)

	return TextResult(content), nil
}
