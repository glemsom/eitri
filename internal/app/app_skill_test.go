package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
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
// as active.
func TestTUISlashSkillThroughEngineSeam(t *testing.T) {
	ws := t.TempDir()
	// Isolate from any real user-global skills so only the project pack below
	// is discoverable.
	t.Setenv("HOME", t.TempDir())
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
	if len(surface.Items) != 1 || surface.Items[0].Name != "tui-skill" {
		t.Fatalf("surface items = %+v, want the tui-skill entry", surface.Items)
	}

	payload, err := surface.Activate(context.Background(), "tui-skill")
	if err != nil {
		t.Fatalf("slash activation error = %v, want nil", err)
	}
	if !strings.Contains(payload, "<skill_content name=\"tui-skill\">") || !strings.Contains(payload, "Do the tui thing") {
		t.Fatalf("slash activation payload wrong:\n%s", payload)
	}
	// The catalog reflects the skill as active; the rail shows no skill state
	// (issue #188), so there is no panel accessor to check.
	if !skills.IsActive("tui-skill") {
		t.Fatalf("tui-skill not marked active after slash activation")
	}
}

// TestTUISlashListsHiddenSkill verifies a disable-model-invocation skill still
// surfaces on the slash `/` completion list and stays activatable from the TUI —
// it is hidden only from the model's automated invocation, never from the human
// slash surface. This is the exact symptom where `/improve-codebase-architecture`
// failed to suggest a present command skill.
func TestTUISlashListsHiddenSkill(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a command skill\ndisable-model-invocation: true\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	skills := discoverSkills(ws)
	reg := tools.NewRegistry(tools.Deps{
		Workspace: ws,
		TempHost:  t.TempDir(),
		GUID:      tools.GUID("slash-hidden-" + t.Name()),
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil (hidden command skill must back the slash surface)")
	}
	var names []string
	for _, it := range surface.Items {
		names = append(names, it.Name)
	}
	if !slices.Contains(names, "improve-codebase-architecture") {
		t.Fatalf("slash completion items = %v, want the hidden command skill suggested", names)
	}

	payload, err := surface.Activate(context.Background(), "improve-codebase-architecture")
	if err != nil {
		t.Fatalf("slash activation of hidden skill error = %v, want nil", err)
	}
	if !strings.Contains(payload, "<skill_content name=\"improve-codebase-architecture\">") || !strings.Contains(payload, "Do the architecture thing") {
		t.Fatalf("slash activation payload wrong:\n%s", payload)
	}

	// The model must still not be able to auto-invoke it: its name is not in
	// the skill tool's enum.
	var defs []tools.Definition
	for _, d := range reg.Definitions() {
		if d.Name == "skill" {
			defs = append(defs, d)
		}
	}
	if len(defs) != 1 {
		t.Fatalf("skill tool definitions = %d, want 1", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	nameProp, _ := props["name"].(map[string]any)
	enum, _ := nameProp["enum"].([]any)
	for _, e := range enum {
		if e == "improve-codebase-architecture" {
			t.Fatalf("skill enum %v must exclude the hidden command skill", enum)
		}
	}
}

// TestDiscoverSkillsUserGlobalRoot verifies discoverSkills resolves the
// user-global skill root as ~/.agents/skills (dot-prefixed, the Agent Skills
// convention), not ~/agents/skills, so user-installed skills are discoverable.
func TestDiscoverSkillsUserGlobalRoot(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, ".agents", "skills", "user-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: user-skill\ndescription: a user-global demo\n---\n\n# User Skill\n\nDo the user thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	t.Setenv("HOME", home)
	skills := discoverSkills(t.TempDir())
	if skills == nil || skills.Skill("user-skill") == nil {
		names := "<nil>"
		if skills != nil {
			names = strings.Join(skills.Names(), ",")
		}
		t.Fatalf("user-global skill not discovered; catalog names = %s, want user-skill", names)
	}
	if got := skills.Scope("user-skill"); got != "user" {
		t.Fatalf("user-skill scope = %q, want user", got)
	}
}

// TestTUISlashHiddenSkillThroughEngineSeamWithArgs closes AC criterion 4 of
// issue #240: a hidden (disable-model-invocation) command skill must remain
// slash-activatable WITH args through the real engine/skill surface, and the
// trailing args must be threaded verbatim to the TUI Turn seam as a follow-up
// user turn. The TUI-level tests (TestModel_slashSkillWithArgs et al.) exercise
// the same flow against a fake my-skill surface, and TestTUISlashListsHiddenSkill
// proves the hidden skill surfaces + bare activation at the app level — but
// neither drives a hidden skill's args path end to end. This test wires a real
// SkillsSurface built by skillSurface from the discovered hidden skill and a
// recording Turn stub, then types `/improve-codebase-architecture <args>` and
// asserts the args land verbatim on the turn seam in note-then-args ordering.
func TestTUISlashHiddenSkillThroughEngineSeamWithArgs(t *testing.T) {
	const wantArgs = "Let us improve this codebase"

	ws := t.TempDir()
	// Isolate from any real user-global skills so only the project pack below
	// is discoverable.
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a command skill\ndisable-model-invocation: true\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
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
		GUID:      tools.GUID("slash-hidden-args-" + t.Name()),
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil (hidden command skill must back the slash surface)")
	}
	var names []string
	for _, it := range surface.Items {
		names = append(names, it.Name)
	}
	if !slices.Contains(names, "improve-codebase-architecture") {
		t.Fatalf("slash completion items = %v, want the hidden command skill suggested", names)
	}

	// Drive a TUI model with the real surface and a recording Turn seam, exactly
	// as runTUI wires them (but with the engine turn replaced by a recording
	// stub). The recording asserts the args reach the turn seam verbatim.
	var turnPrompts []string
	m := tui.NewModelCfg(tui.Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (tui.TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return tui.TurnResult{Answer: "ok"}, nil
		},
		Skills: surface,
	})
	m = appTestResize(t, m)
	m = appTestTypeText(t, m, "/improve-codebase-architecture "+wantArgs)
	m = appTestSubmitAndWait(t, m)

	// (a) The args turn lands verbatim on the seam — the core of criterion 4.
	if len(turnPrompts) != 1 || turnPrompts[0] != wantArgs {
		t.Fatalf("args turn seam = %v, want [%q]", turnPrompts, wantArgs)
	}

	// (b) The skill activation payload renders as a note BEFORE the args user
	// turn (note-then-args ordering, mirroring TestModel_slashSkillWithArgs).
	content := appTestANSIStrip(appTestView(m))
	n := strings.Index(content, "<skill_content name=\"improve-codebase-architecture\">")
	if n < 0 {
		t.Fatalf("skill activation payload not rendered;\n%s", content)
	}
	if strings.Index(content, "Do the architecture thing") < 0 {
		t.Fatalf("skill body not rendered;\n%s", content)
	}
	if a := strings.Index(content, wantArgs); a < 0 {
		t.Fatalf("args message not rendered;\n%s", content)
	} else if n > a {
		t.Fatalf("skill note index %d must precede args index %d (note renders before args turn)", n, a)
	}

	// (c) The catalog reflects the hidden skill as active after slash activation.
	if !skills.IsActive("improve-codebase-architecture") {
		t.Fatalf("improve-codebase-architecture not marked active after slash activation")
	}

	// (d) The hidden skill stays excluded from the model-invoker `skill` tool
	// enum — hidden from the model, available to the human slash surface.
	var defs []tools.Definition
	for _, d := range reg.Definitions() {
		if d.Name == "skill" {
			defs = append(defs, d)
		}
	}
	if len(defs) != 1 {
		t.Fatalf("skill tool definitions = %d, want 1", len(defs))
	}
	props, _ := defs[0].Parameters["properties"].(map[string]any)
	nameProp, _ := props["name"].(map[string]any)
	enum, _ := nameProp["enum"].([]any)
	for _, e := range enum {
		if e == "improve-codebase-architecture" {
			t.Fatalf("skill enum %v must exclude the hidden command skill", enum)
		}
	}
}

// appTestResize resizes a TUI model to the standard test viewport so rendering
// assertions are stable. It is the app-package twin of the tui test helper
// resize; the tui helpers are package-private, so the app tests that drive a
// real tui.Model through its exported Update/View reproduce the minimal
// keystroke logic here (issue #240).
func appTestResize(t *testing.T, m tui.Model) tui.Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return appTestAsModel(t, nm)
}

// appTestTypeText feeds a run of text to the composer in one keypress, mirroring
// the tui package's typeText helper.
func appTestTypeText(t *testing.T, m tui.Model, s string) tui.Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: s})
	return appTestAsModel(t, nm)
}

// appTestSubmitAndWait feeds Enter to run the turn and then the async completion,
// unwrapping any tea.BatchMsg so the (possibly chained) skill-args follow-up turn
// runs and its message lands. It throttles into the shared appTestRunSubmitted
// helper, mirroring submitAndWait/runSubmitted from the tui package.
func appTestSubmitAndWait(t *testing.T, m tui.Model) tui.Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	return appTestRunSubmitted(t, appTestAsModel(t, nm), cmd)
}

// appTestRunSubmitted executes a submitted command synchronously, unwrapping a
// tea.BatchMsg, delivering each resulting message, and threading any follow-up
// command so a skill-args turn (queued by the skillDoneMsg handler, issue #239)
// chains through to its Turn seam invocation.
func appTestRunSubmitted(t *testing.T, m tui.Model, cmd tea.Cmd) tui.Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, c := range bm {
			m = appTestRunSubmitted(t, m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	nm, next := m.Update(msg)
	m = appTestAsModel(t, nm)
	return appTestRunSubmitted(t, m, next)
}

// appTestAsModel type-asserts a tea.Model from Update back to the concrete
// tui.Model, mirroring the tui package's asModel helper.
func appTestAsModel(t *testing.T, tm tea.Model) tui.Model {
	t.Helper()
	md, ok := tm.(tui.Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want tui.Model", tm)
	}
	return md
}

// appTestView renders the model's current view content.
func appTestView(m tui.Model) string { return m.View().Content }

// appTestANSIStrip removes SGR escape sequences so content-ordering assertions
// aren't derailed by style runs between words (mirrors tui's ansiStrip).
func appTestANSIStrip(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
}

// captureSkillRequests records every provider.Request the engine issues for the
// slash-args turn, so a test can assert the injected skill body is present in the
// request Messages (issue #260).
type captureSkillRequests struct {
	reqs []provider.Request
}

// TestTUISlashArgsPutsSkillInProviderContext closes the root cause of issue
// #260 at the app/engine seam: after `/skillname <args>`, the provider request
// for the args turn must carry the skill's <skill_content> payload (plus any
// <skill_resources>) ahead of the user args. It drives a real tui.Model through
// the real runEngineTurn adapter (not a recording Turn stub) wired to a scripted
// provider, so it asserts the exact provider Messages the model would send — the
// criterion the Turn-stub tests cannot see.
func TestTUISlashArgsPutsSkillInProviderContext(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a command skill\ndisable-model-invocation: true\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
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
		GUID:      tools.GUID("slash-provider-" + t.Name()),
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatal("skillSurface = nil, want non-nil")
	}

	// The model's args turn flows into the provider via the real runEngineTurn
	// seam. Capture every provider request so we can assert the skill body sits in
	// the message list.
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	turn := runEngineTurn(e, cfg, reg, "sess-"+t.Name(), nil)
	m := tui.NewModelCfg(tui.Dependencies{
		Turn:   turn,
		Skills: surface,
	})
	m = appTestResize(t, m)
	m = appTestTypeText(t, m, "/improve-codebase-architecture Let us improve this")
	m = appTestSubmitAndWait(t, m)

	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests for the args turn")
	}
	// The args turn is the only engine turn the activation queues.
	msgs := cap.reqs[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("provider Messages = %d, want 3 (system + skill inject + user args); got %v", len(msgs), msgs)
	}
	if msgs[1].Role != provider.RoleSystem {
		t.Errorf("Messages[1].Role = %q, want %q (skill injected as a system prefix)", msgs[1].Role, provider.RoleSystem)
	}
	if !strings.Contains(msgs[1].Content, "<skill_content name=\"improve-codebase-architecture\">") {
		t.Errorf("Messages[1] lacks the skill_content payload:\n%s", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "Do the architecture thing") {
		t.Errorf("Messages[1] lacks the skill body:\n%s", msgs[1].Content)
	}
	if msgs[2].Role != provider.RoleUser || msgs[2].Content != "Let us improve this" {
		t.Errorf("Messages[2] = %+v, want the user args turn", msgs[2])
	}

	// The catalog reflects the skill as active after slash activation.
	if !skills.IsActive("improve-codebase-architecture") {
		t.Fatal("skill not marked active after slash activation")
	}
}
