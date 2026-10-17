package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/skillindex"
	"github.com/glemsom/eitri/internal/tools"
)

// skillTestRegistry builds a registry with one model-invocable `tdd` skill and
// returns it with the skill directory.
func skillTestRegistry(t *testing.T) (*tools.Registry, string) {
	t.Helper()
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	skillDir := filepath.Join(projectRoot, "tdd")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	md := "---\nname: tdd\ndescription: test first\n---\n\nUNIQUE-BODY-MARKER\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	catalog, err := tools.Discover(t.TempDir(), projectRoot, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if catalog.Skill("tdd") == nil {
		t.Fatal("tdd skill was not discovered")
	}
	reg, err := tools.NewRegistry(tools.Deps{
		Workspace: workspace,
		TempHost:  t.TempDir(),
		Runner:    tools.RealRunner,
		Skills:    catalog,
	})
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}
	return reg, skillDir
}

func skillToolDef() []provider.Tool {
	return []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "skill", Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []any{"name"},
	}}}}
}

// TestDispatchSkillToolHandsOffActiveSet guards the run-loop seam end to end: the
// `skill` tool returns the directory-tagged payload on first activation, and the
// engine's scan of the conversation makes a second activation short-circuit to
// the already-active note instead of re-injecting the body.
func TestDispatchSkillToolHandsOffActiveSet(t *testing.T) {
	t.Parallel()
	reg, skillDir := skillTestRegistry(t)

	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResults[:0]
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults = append(toolResults, m.Content)
			}
		}
		switch len(toolResults) {
		case 0:
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "skill", Arguments: `{"name":"tdd"}`},
			}, Done: true}), nil
		case 1:
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c2", Name: "skill", Arguments: `{"name":"tdd"}`},
			}, Done: true}), nil
		default:
			return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
		}
	})

	e := New(scripted, &mockTranscript{})
	_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "go"}, AgentOptions{
		Tools:    skillToolDef(),
		Executor: executor(reg),
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool results = %d, want 2:\n%v", len(toolResults), toolResults)
	}
	if want := skillindex.ActivationOpen("tdd", skillDir); !strings.Contains(toolResults[0], want) {
		t.Fatalf("first activation missing %q:\n%s", want, toolResults[0])
	}
	if !strings.Contains(toolResults[0], "UNIQUE-BODY-MARKER") {
		t.Fatalf("first activation dropped the body:\n%s", toolResults[0])
	}
	if !strings.Contains(toolResults[1], skillDir) || !strings.Contains(toolResults[1], "already active") {
		t.Fatalf("second activation did not short-circuit with the directory note:\n%s", toolResults[1])
	}
	if strings.Contains(toolResults[1], "UNIQUE-BODY-MARKER") {
		t.Fatalf("second activation re-injected the body:\n%s", toolResults[1])
	}
}

// TestDispatchSkillToolDedupesSameTurnCalls guards same-turn dedup: two `skill`
// calls for one skill in a single turn must yield exactly one full payload and
// one already-active note. The active set is snapshotted from the conversation
// before the turn's calls run, so without an intra-turn claim both calls deliver
// the body and the instructions are injected twice.
func TestDispatchSkillToolDedupesSameTurnCalls(t *testing.T) {
	t.Parallel()
	reg, skillDir := skillTestRegistry(t)

	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResultContents(req.Messages)
		if len(toolResults) == 0 {
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "skill", Arguments: `{"name":"tdd"}`},
				{ID: "c2", Name: "skill", Arguments: `{"name":"tdd"}`},
			}, Done: true}), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	})

	e := New(scripted, &mockTranscript{})
	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "go"}, AgentOptions{
		Tools:    skillToolDef(),
		Executor: executor(reg),
		MaxTurns: 5,
	}); err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool results = %d, want 2:\n%v", len(toolResults), toolResults)
	}
	payloads, notes := 0, 0
	for _, r := range toolResults {
		switch {
		case strings.Contains(r, "UNIQUE-BODY-MARKER"):
			payloads++
		case strings.Contains(r, skillDir) && strings.Contains(r, "already active"):
			notes++
		}
	}
	if payloads != 1 || notes != 1 {
		t.Fatalf("same-turn duplicate calls: payloads = %d, notes = %d, want 1 and 1:\n%v", payloads, notes, toolResults)
	}
}

// TestDispatchSkillToolCountsHumanInjectionActive guards the human path's
// activity: a skill pinned into the user layer by `/skillname` must make the
// model's `skill` call short-circuit instead of duplicating the body.
func TestDispatchSkillToolCountsHumanInjectionActive(t *testing.T) {
	t.Parallel()
	reg, skillDir := skillTestRegistry(t)
	injected, err := reg.ActivateSkill(context.Background(), "tdd")
	if err != nil {
		t.Fatalf("ActivateSkill error = %v, want nil", err)
	}
	payload := injected.Text

	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResults[:0]
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults = append(toolResults, m.Content)
			}
		}
		if len(toolResults) == 0 {
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "skill", Arguments: `{"name":"tdd"}`},
			}, Done: true}), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	})

	e := New(scripted, &mockTranscript{})
	_, err = e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "go", SkillInject: &payload}, AgentOptions{
		Tools:    skillToolDef(),
		Executor: executor(reg),
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(toolResults) != 1 {
		t.Fatalf("tool results = %d, want 1:\n%v", len(toolResults), toolResults)
	}
	if !strings.Contains(toolResults[0], skillDir) || !strings.Contains(toolResults[0], "already active") {
		t.Fatalf("human-pinned skill did not count as active:\n%s", toolResults[0])
	}
	if strings.Contains(toolResults[0], "UNIQUE-BODY-MARKER") {
		t.Fatalf("human-pinned skill was re-injected by the tool:\n%s", toolResults[0])
	}
}

// oversizedSkillRegistry builds a registry with one model-invocable `bulk` skill
// whose body exceeds the tool-result byte cap, ending in a unique marker so a
// tail truncation is observable. It returns the registry and skill directory.
func oversizedSkillRegistry(t *testing.T) (*tools.Registry, string) {
	t.Helper()
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	skillDir := filepath.Join(projectRoot, "bulk")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	md := "---\nname: bulk\ndescription: oversized body\n---\n\n" +
		strings.Repeat("filler line to exceed the tool-result byte cap\n", 2500) +
		"UNIQUE-END-MARKER\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	catalog, err := tools.Discover(t.TempDir(), projectRoot, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Discover error = %v, want nil", err)
	}
	if catalog.Skill("bulk") == nil {
		t.Fatal("bulk skill was not discovered")
	}
	reg, err := tools.NewRegistry(tools.Deps{
		Workspace: workspace,
		TempHost:  t.TempDir(),
		Runner:    tools.RealRunner,
		Skills:    catalog,
	})
	if err != nil {
		t.Fatalf("NewRegistry error = %v, want nil", err)
	}
	return reg, skillDir
}

// TestDispatchSkillToolDeliversOversizedBodyWhole guards the tool-result byte
// cap's skill exemption end to end: an activation larger than
// compress.DefaultByteCap must reach the model untruncated (its tail marker
// survives), so a later activation still returns the already-active note rather
// than falsely claiming an unreachable body is in context.
func TestDispatchSkillToolDeliversOversizedBodyWhole(t *testing.T) {
	t.Parallel()
	reg, skillDir := oversizedSkillRegistry(t)

	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResults[:0]
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults = append(toolResults, m.Content)
			}
		}
		switch len(toolResults) {
		case 0:
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "skill", Arguments: `{"name":"bulk"}`},
			}, Done: true}), nil
		case 1:
			return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c2", Name: "skill", Arguments: `{"name":"bulk"}`},
			}, Done: true}), nil
		default:
			return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
		}
	})

	e := New(scripted, &mockTranscript{})
	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "go"}, AgentOptions{
		Tools:    skillToolDef(),
		Executor: executor(reg),
		MaxTurns: 5,
	}); err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool results = %d, want 2", len(toolResults))
	}
	if want := skillindex.ActivationOpen("bulk", skillDir); !strings.Contains(toolResults[0], want) {
		t.Fatalf("first activation missing %q", want)
	}
	if !strings.Contains(toolResults[0], "UNIQUE-END-MARKER") {
		t.Fatalf("oversized activation lost its tail to the %d-byte tool-result cap", compress.DefaultByteCap)
	}
	if len(toolResults[0]) <= compress.DefaultByteCap {
		t.Fatalf("first activation delivered %d bytes, want more than the %d-byte cap", len(toolResults[0]), compress.DefaultByteCap)
	}
	if !strings.Contains(toolResults[1], skillDir) || !strings.Contains(toolResults[1], "already active") {
		t.Fatalf("second activation did not short-circuit with the directory note:\n%s", toolResults[1])
	}
	if strings.Contains(toolResults[1], "UNIQUE-END-MARKER") {
		t.Fatalf("second activation re-injected the body")
	}
}
