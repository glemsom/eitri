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
	return skillRegistryWithBody(t, "UNIQUE-BODY-MARKER\n")
}

// skillRegistryWithBody builds the same one-skill registry with the given body,
// so a body larger than the tool-result byte cap can be exercised end to end.
func skillRegistryWithBody(t *testing.T, body string) (*tools.Registry, string) {
	t.Helper()
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, ".agents", "skills")
	skillDir := filepath.Join(projectRoot, "tdd")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	md := "---\nname: tdd\ndescription: test first\n---\n\n" + body
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

// TestDispatchSkillToolReactivatesTruncatedOversizedBody guards the byte-cap
// seam end to end: an activation whose payload exceeds the tool-result cap is
// head-truncated, losing the closing tag and body tail, so the next activation
// must re-deliver the instructions with the truncation marker instead of
// short-circuiting to a note that claims the full body is already in context.
func TestDispatchSkillToolReactivatesTruncatedOversizedBody(t *testing.T) {
	t.Parallel()
	reg, skillDir := skillRegistryWithBody(t, strings.Repeat("OVERSIZED-BODY-LINE\n", compress.DefaultByteCap/20))

	var toolResults []string
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolResults = toolResultContents(req.Messages)
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

	first := toolResults[0]
	if len(first) > compress.DefaultByteCap {
		t.Fatalf("first activation is %d bytes, exceeds the %d-byte cap", len(first), compress.DefaultByteCap)
	}
	if !strings.Contains(first, "bytes truncated") {
		t.Fatalf("first activation was truncated without telling the model:\n%s", first)
	}
	if want := skillindex.ActivationOpen("tdd", skillDir); !strings.Contains(first, want) {
		t.Fatalf("truncated activation dropped the directory-tagged head %q:\n%s", want, first)
	}

	second := toolResults[1]
	if strings.Contains(second, "already active") {
		t.Fatalf("re-activation of a truncated skill short-circuited to the already-active note:\n%s", second)
	}
	if want := skillindex.ActivationOpen("tdd", skillDir); !strings.Contains(second, want) {
		t.Fatalf("re-activation did not re-deliver the skill payload %q:\n%s", want, second)
	}
}
