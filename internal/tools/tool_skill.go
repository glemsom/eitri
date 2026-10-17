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
const skillToolDescription = "Activate one of the available skills by name. The skill index (an <available_skills> block) is re-sent each turn; pass a listed skill's exact name to load its full instructions and the absolute directory holding its bundled resources. Prefer this over reading a SKILL.md with bash. A skill is already active while its body is present in the conversation — activated by this tool earlier, or invoked by the user as /skillname — and returns a short note instead of re-injecting the body; a second same-named call in one turn likewise gets the note. The body is delivered whole and never truncated, so re-activate only after compaction drops it or in a new session. Human-only skills cannot be activated."

// skillTool is the model-facing `skill` tool: it resolves a name against the
// merged catalog, applies the model-invocability gate, and claims the name
// against the turn's activation set (supplied by the engine through the context)
// so a skill already present in the message history — or already delivered
// earlier in the same turn — returns the short note instead of its body.
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
	if !skillindex.ClaimActivation(ctx, name) {
		return compress.ToolResult{Text: alreadyActiveSkillNote(name, sk.Dir), Uncapped: true}, nil
	}
	return compress.ToolResult{Text: renderSkillPayload(name, sk), Uncapped: true}, nil
}

// alreadyActiveSkillNote is the short, non-error reply for a repeat activation:
// it repeats the skill directory so the model can still reach bundled resources
// without re-injecting the body.
func alreadyActiveSkillNote(name, dir string) string {
	return fmt.Sprintf("Skill %q is already active; its instructions are above in the conversation. Directory: %s. Re-activate only after compaction drops the body or in a new session.", name, dir)
}
