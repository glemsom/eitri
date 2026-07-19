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
	return "Activate a skill by name. Skills provide reusable instructions, references, and scripts for specialized tasks. Call this when a task matches an available skill description. Returns structured skill content including instructions and resource manifest."
}

func (t *SkillTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *SkillTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed skillArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("skill: invalid args: %w", err), false
	}

	if parsed.Name == "" {
		return textBlocks("Error: skill name is required"), nil, true
	}

	if t.skillsSvc == nil {
		return textBlocks("Error: skills service not available"), nil, true
	}

	skill := t.skillsSvc.Lookup(parsed.Name)
	if skill == nil {
		// Check if disabled
		if t.skillsSvc.IsDisabled(parsed.Name) {
			return textBlocks(fmt.Sprintf("Error: skill %q is disabled. Enable it from the Skills page.", parsed.Name)), nil, true
		}
		return textBlocks(fmt.Sprintf("Error: skill %q not found in effective skills", parsed.Name)), nil, true
	}

	// Record activation in session for persistence across runs
	if t.uiSessionMgr != nil {
		sessionID, _ := ctx.Value(sessionIDKey).(string)
		if sessionID != "" {
			t.uiSessionMgr.ActivateSkill(sessionID, parsed.Name)
		}
	}

	resources := skills.ListResources(skill.Path)
	content := skills.SkillContent(skill.Body, resources, skill.Path)

	return textBlocks(content), nil, false
}
