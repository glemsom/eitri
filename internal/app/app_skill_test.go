package app

import (
	"context"
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

func TestTUISlashSkillThroughEngineSeam(t *testing.T) {
	ws := t.TempDir()
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
}

func TestTUISlashRepeatedActivationReapplies(t *testing.T) {
	ws := t.TempDir()
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
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil for a discovered skill")
	}

	first, err := surface.Activate(context.Background(), "tui-skill")
	if err != nil {
		t.Fatalf("first slash activation error = %v, want nil", err)
	}
	if !strings.Contains(first, "Do the tui thing") {
		t.Fatalf("first slash activation payload wrong:\n%s", first)
	}

	// A repeated slash must re-apply the full skill body, not short-circuit to
	// the model-tool "already active" dedupe notice.
	second, err := surface.Activate(context.Background(), "tui-skill")
	if err != nil {
		t.Fatalf("second slash activation error = %v, want nil", err)
	}
	if !strings.Contains(second, "Do the tui thing") {
		t.Fatalf("second slash activation must re-inject the skill body, got:\n%s", second)
	}
	if strings.Contains(second, "already active") {
		t.Fatalf("second slash activation returned the dedupe notice instead of re-applying:\n%s", second)
	}
}

func TestTUISlashListsHiddenSkill(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a slash-invocable command skill\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	skills := discoverSkills(ws)
	reg := tools.NewRegistry(tools.Deps{
		Workspace: ws,
		TempHost:  t.TempDir(),
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil for a discovered skill backing the slash surface")
	}
	var names []string
	for _, it := range surface.Items {
		names = append(names, it.Name)
	}
	if !slices.Contains(names, "improve-codebase-architecture") {
		t.Fatalf("slash completion items = %v, want the command skill suggested", names)
	}

	payload, err := surface.Activate(context.Background(), "improve-codebase-architecture")
	if err != nil {
		t.Fatalf("slash activation error = %v, want nil", err)
	}
	if !strings.Contains(payload, "<skill_content name=\"improve-codebase-architecture\">") || !strings.Contains(payload, "Do the architecture thing") {
		t.Fatalf("slash activation payload wrong:\n%s", payload)
	}

}

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

func TestTUISlashHiddenSkillThroughEngineSeamWithArgs(t *testing.T) {
	const wantArgs = "Let us improve this codebase"

	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a slash-invocable command skill\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
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
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatalf("skillSurface = nil, want non-nil for a discovered skill backing the slash surface")
	}
	var names []string
	for _, it := range surface.Items {
		names = append(names, it.Name)
	}
	if !slices.Contains(names, "improve-codebase-architecture") {
		t.Fatalf("slash completion items = %v, want the command skill suggested", names)
	}

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
	appTestSubmitAndWait(t, m)

	if len(turnPrompts) != 1 || turnPrompts[0] != wantArgs {
		t.Fatalf("args turn seam = %v, want [%q]", turnPrompts, wantArgs)
	}

	content := appTestANSIStrip(appTestView(m))
	if strings.Contains(content, "<skill_content name=\"improve-codebase-architecture\">") {
		t.Fatalf("skill activation payload must not be echoed as a note (single delivery via injection);\n%s", content)
	}
	if a := strings.Index(content, wantArgs); a < 0 {
		t.Fatalf("args message not rendered;\n%s", content)
	}

}

func appTestResize(t *testing.T, m tui.Model) tui.Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return appTestAsModel(t, nm)
}

func appTestTypeText(t *testing.T, m tui.Model, s string) tui.Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: s})
	return appTestAsModel(t, nm)
}

func appTestSubmitAndWait(t *testing.T, m tui.Model) tui.Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	return appTestRunSubmitted(t, appTestAsModel(t, nm), cmd)
}

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

func appTestAsModel(t *testing.T, tm tea.Model) tui.Model {
	t.Helper()
	md, ok := tm.(tui.Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want tui.Model", tm)
	}
	return md
}

func appTestView(m tui.Model) string { return m.View().Content }

func appTestANSIStrip(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
}

type captureSkillRequests struct {
	reqs []provider.Request
}

func TestTUISlashArgsPutsSkillInProviderContext(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a slash-invocable command skill\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
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
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatal("skillSurface = nil, want non-nil")
	}

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	turn := runEngineTurn(e, func() config.Config { return cfg }, reg, tui.NewLiveSessionKey("sess-"+t.Name()), skills, nil)
	m := tui.NewModelCfg(tui.Dependencies{
		Turn:   turn,
		Skills: surface,
	})
	m = appTestResize(t, m)
	m = appTestTypeText(t, m, "/improve-codebase-architecture Let us improve this")
	appTestSubmitAndWait(t, m)

	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests for the args turn")
	}
	msgs := cap.reqs[0].Messages
	if len(msgs) != 4 {
		t.Fatalf("provider Messages = %d, want 4 (system head + workspace + skill index + user); got %v", len(msgs), msgs)
	}
	if msgs[1].Role != provider.RoleSystem || !strings.Contains(msgs[1].Content, "## Working directory") {
		t.Errorf("Messages[1] should be the working-directory directive: %v", msgs[1])
	}
	if msgs[2].Role != provider.RoleSystem || !strings.Contains(msgs[2].Content, "<available_skills>") {
		t.Errorf("Messages[2] should be the skill index: %v", msgs[2])
	}

	if msgs[3].Role != provider.RoleUser {
		t.Errorf("Messages[3].Role = %q, want %q (slash skill in the high-priority user layer, not a second system message)", msgs[3].Role, provider.RoleUser)
	}
	if !strings.Contains(msgs[3].Content, "<skill_content name=\"improve-codebase-architecture\">") {
		t.Errorf("Messages[3] lacks the skill_content payload:\n%s", msgs[3].Content)
	}
	if !strings.Contains(msgs[3].Content, "Do the architecture thing") {
		t.Errorf("Messages[3] lacks the skill body:\n%s", msgs[3].Content)
	}
	if !strings.Contains(msgs[3].Content, "binding") {
		t.Errorf("Messages[3] lacks the binding framing:\n%s", msgs[3].Content)
	}
	if !strings.Contains(msgs[3].Content, "Let us improve this") {
		t.Errorf("Messages[3] lacks the user args prompt delivered adjacently:\n%s", msgs[3].Content)
	}

}

func TestTUISlashBarePutsSkillInProviderContext(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "improve-codebase-architecture")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: improve-codebase-architecture\ndescription: a slash-invocable command skill\n---\n\n# Improve Codebase\n\nDo the architecture thing.\n"
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
		Skills:    skills,
	})
	surface := skillSurface(reg, skills)
	if surface == nil {
		t.Fatal("skillSurface = nil, want non-nil")
	}

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	turn := runEngineTurn(e, func() config.Config { return cfg }, reg, tui.NewLiveSessionKey("sess-"+t.Name()), skills, nil)
	m := tui.NewModelCfg(tui.Dependencies{
		Turn:   turn,
		Skills: surface,
	})
	m = appTestResize(t, m)
	m = appTestTypeText(t, m, "/improve-codebase-architecture")
	appTestSubmitAndWait(t, m)

	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests for the bare slash turn")
	}
	msgs := cap.reqs[0].Messages
	if len(msgs) != 4 {
		t.Fatalf("provider Messages = %d, want 4 (system head + workspace + skill index + user); got %v", len(msgs), msgs)
	}
	if msgs[1].Role != provider.RoleSystem || !strings.Contains(msgs[1].Content, "## Working directory") {
		t.Errorf("Messages[1] should be the working-directory directive: %v", msgs[1])
	}
	if msgs[3].Role != provider.RoleUser {
		t.Errorf("Messages[3].Role = %q, want %q (slash skill in the high-priority user layer, not a second system message)", msgs[3].Role, provider.RoleUser)
	}
	if !strings.Contains(msgs[3].Content, "<skill_content name=\"improve-codebase-architecture\">") {
		t.Errorf("Messages[3] lacks the skill_content payload:\n%s", msgs[3].Content)
	}
	if !strings.Contains(msgs[3].Content, "apply the improve-codebase-architecture skill") {
		t.Errorf("Messages[3] lacks the bare-slash default prompt delivered adjacently:\n%s", msgs[3].Content)
	}

}
