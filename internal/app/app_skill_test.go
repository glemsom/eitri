package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/provider"
)

// scriptedSkillTurn drives the engine seam through a provider that (1) activates
// my-skill via the enum-constrained skill tool, (2) re-activates the same skill
// to exercise dedupe, then (3) reports both tool results in a final answer. It
// validates that the request head carries the enum-constrained skill tool.
func scriptedSkillTurn(t testing.TB) *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults []string
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults = append(toolResults, m.Content)
			}
		}
		switch len(toolResults) {
		case 0: // first turn: activate the skill
			if err := assertSkillEnum(req.Tools); err != nil {
				return nil, err
			}
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "activate the skill"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_s1", Name: "skill", Arguments: `{"name":"my-skill"}`},
				}, Done: true},
			), nil
		case 1: // second turn: re-activate the same skill to exercise dedupe
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "activate again"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_s2", Name: "skill", Arguments: `{"name":"my-skill"}`},
				}, Done: true},
			), nil
		default: // third turn: report what the tool results carried
			return provider.StreamFunc(
				provider.Chunk{Content: "first=" + toolResults[0] + " second=" + toolResults[1]},
				provider.Chunk{FinishReason: "stop", Done: true},
			), nil
		}
	})
}

func assertSkillEnum(tools []provider.Tool) error {
	for _, tl := range tools {
		if tl.Function.Name != "skill" {
			continue
		}
		props, _ := tl.Function.Parameters["properties"].(map[string]any)
		nameProp, _ := props["name"].(map[string]any)
		enum, _ := nameProp["enum"].([]any)
		for _, e := range enum {
			if e == "my-skill" {
				return nil
			}
		}
		return errors.New("skill tool enum does not include my-skill")
	}
	return errors.New("request head missing skill tool")
}

// TestBatchSkillThroughEngineSeam runs batch mode with a fake provider driving a
// skill activation, re-activation, and final answer through the engine seam. It
// verifies the enum-constrained skill tool is present in the request head, the
// first activation returns the structured agentskills-io wrap (body plus
// resources), and re-activation dedupes (returns a short notice, not the body
// again). It depends on a project-scoped skill under <workspace>/.agents/skills.
func TestBatchSkillThroughEngineSeam(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-skill")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	// Project-scoped skill pack: <ws>/.agents/skills/my-skill/SKILL.md.
	skillDir := filepath.Join(ws, ".agents", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: my-skill\ndescription: A demo skill for testing activation\n---\n\n# My Skill\n\nFollow these instructions carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		t.Fatalf("mkdir references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("guide\n"), 0o600); err != nil {
		t.Fatalf("write resource: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	defer os.RemoveAll(ws)

	var out bytes.Buffer
	dir := t.TempDir()
	err = Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Provider: scriptedSkillTurn(t),
		Prompt:   "apply the skill",
		Stdout:   &out,
	})
	if err != nil {
		t.Fatalf("Run(batch skill) error = %v, want nil", err)
	}
	got := out.String()
	// The request head carried the skill tool with an enum-constrained name,
	// and the first activation returned the wrapped body + resource listing.
	if !strings.Contains(got, "<skill_content name=\"my-skill\">") {
		t.Fatalf("skill activation payload missing skill_content wrap:\n%s", got)
	}
	if !strings.Contains(got, "Follow these instructions carefully") {
		t.Fatalf("skill body not injected through tool result:\n%s", got)
	}
	if !strings.Contains(got, "references/guide.md") {
		t.Fatalf("skill resources not advertised:\n%s", got)
	}
	// Dedupe: the second activation must not re-inject the body.
	if strings.Count(got, "Follow these instructions carefully") != 1 {
		t.Fatalf("skill body appeared more than once (dedupe failed):\n%s", got)
	}
	if !strings.Contains(got, "already active") {
		t.Fatalf("re-activation did not produce a dedupe notice:\n%s", got)
	}
}

// TestTUISlashSkillThroughEngineSeam verifies the TUI slash-command activation
// path (T9b) runs through the same `skill` tool the batch engine uses (T8): the
// SkillsSurface built by skillSurface drives reg.Run(ctx, "skill", ...) and
// returns the wrapped agentskills-io payload, and the catalog reflects the skill
// as active (docs/spec.md §9, eitri.md §2.3).
func TestTUISlashSkillThroughEngineSeam(t *testing.T) {
	ws := t.TempDir()
	skillDir := filepath.Join(ws, ".agents", "skills", "tui-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: tui-skill\ndescription: a slash-invocable demo\n---\n\n# TUI Skill\n\nDo the tui thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	skills := discoverSkills(ws)
	reg := tools.NewRegistry(tools.Deps{
		Workspace: ws,
		TempHost:  t.TempDir(),
		GUID:      tools.GUID("tui-seam-" + t.Name()),
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil for a discovered skill")
	}
	if len(surface.Items) != 1 || surface.Items[0].Name != "tui-skill" || surface.Items[0].Scope != "project" {
		t.Fatalf("surface items = %+v, want the project-scoped tui-skill", surface.Items)
	}

	payload, err := surface.Activate(context.Background(), "tui-skill")
	if err != nil {
		t.Fatalf("slash activation error = %v, want nil", err)
	}
	if !strings.Contains(payload, "<skill_content name=\"tui-skill\">") || !strings.Contains(payload, "Do the tui thing") {
		t.Fatalf("slash activation payload wrong:\n%s", payload)
	}
	// The catalog and its panel items reflect the skill as active.
	if !skills.IsActive("tui-skill") {
		t.Fatalf("tui-skill not marked active after slash activation")
	}
	foundActive := false
	for _, it := range skills.Items() {
		if it.Name == "tui-skill" && it.Active {
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatalf("panel items do not reflect tui-skill as active")
	}
}
