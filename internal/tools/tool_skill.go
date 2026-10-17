package tools

import (
	"context"
	"fmt"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/skillindex"
)

// skillToolDescription carries the activation mechanics that used to live in the
// system prompt: the index, calling a skill by name, preferring the tool over
// reading a SKILL.md, and re-activating only after compaction dropped the body.
const skillToolDescription = "Activate one of the available skills by name. The skill index (an <available_skills> block) is re-sent each turn; pass a listed skill's exact name to load its full instructions and the absolute directory holding its bundled resources. Prefer this over reading a SKILL.md with bash. A skill already active in this conversation returns a short note instead of re-injecting its body, so re-activate only after compaction has dropped the instructions. Human-only skills cannot be activated."

// skillTool is the model-facing `skill` tool: it resolves a name against the
// merged catalog, applies the model-invocability gate, and claims the name
// against the turn's activation set (supplied by the engine through the context)
// so a skill already present in the message history — or already delivered
// earlier in the same turn — returns the short note instead of its body. A body
// larger than the tool-result byte cap is never claimed, because its capped
// delivery can never be complete, so it re-activates instead of short-circuiting.
type skillTool struct {
	catalog *Catalog
}

func (s *skillTool) Name() string { return "skill" }

func (s *skillTool) Description() string { return skillToolDescription }

func (s *skillTool) Schema() map[string]any {
	return strictSchema(map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "The exact name of a skill listed in the skill index.",
		},
	}, []string{"name"})
}

func (s *skillTool) Run(ctx context.Context, args map[string]any) (compress.ToolResult, error) {
	name, err := strArg(args, "name")
	if err != nil {
		return compress.ToolResult{}, err
	}
	if s.catalog == nil || len(s.catalog.Names()) == 0 {
		return compress.ToolResult{}, fmt.Errorf("no skills available")
	}
	sk := s.catalog.Skill(name)
	if sk == nil {
		return compress.ToolResult{}, fmt.Errorf("unknown skill %q", name)
	}
	if !sk.ModelInvocable {
		return compress.ToolResult{}, fmt.Errorf("skill %q is not available to the model", name)
	}
	payload := renderSkillPayload(name, sk)
	// A body larger than the tool-result byte cap cannot be fully present in
	// context: the engine head-truncates it, dropping the closing tag and the
	// instructions' tail. Such an activation must not claim the name, or a
	// same-turn repeat would return the already-active note for a body that was
	// never fully delivered; it still short-circuits when the complete body is
	// already present (a human `/skillname` injection is not capped).
	if len(payload) > compress.DefaultByteCap {
		if skillindex.IsSkillActive(ctx, name) {
			return compress.ToolResult{Text: alreadyActiveSkillNote(name, sk.Dir)}, nil
		}
		return compress.ToolResult{Text: payload}, nil
	}
	if !skillindex.ClaimActivation(ctx, name) {
		return compress.ToolResult{Text: alreadyActiveSkillNote(name, sk.Dir)}, nil
	}
	return compress.ToolResult{Text: payload}, nil
}

// alreadyActiveSkillNote is the short, non-error reply for a repeat activation:
// it repeats the skill directory so the model can still reach bundled resources
// without re-injecting the body.
func alreadyActiveSkillNote(name, dir string) string {
	return fmt.Sprintf("Skill %q is already active; its instructions are above. Directory: %s. Re-activate only after compaction drops the body.", name, dir)
}
