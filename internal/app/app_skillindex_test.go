package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/skillindex"
	"github.com/glemsom/eitri/internal/tools"
)

// TestRunAgentInjectsSkillIndex guards the batch/TUI shared seam: once runAgent
// receives the discovered catalog it must render and carry the model-visible
// index to the engine as a dedicated system-layer message, so the model sees
// available skills without perturbing the byte-stable system prompt.
func TestRunAgentInjectsSkillIndex(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	skillDir := filepath.Join(ws, ".agents", "skills", "indexed-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillMD := "---\nname: indexed-skill\ndescription: a model-visible demo\n---\n\n# Indexed Skill\n\nDo the indexed thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	skills := discoverSkills(t.TempDir(), ws)
	reg, _ := tools.NewRegistry(tools.Deps{TempHost: t.TempDir(), Runner: tools.RealRunner, Workspace: ws, Skills: skills})

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", skills, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	msgs := cap.reqs[0].Messages
	var sawIndex bool
	for _, m := range msgs {
		if m.Role == provider.RoleSystem && skillindex.IsBlock(m.Content) {
			if !strings.Contains(m.Content, "indexed-skill") {
				t.Fatalf("skill index system message lacks the model-visible skill:\n%s", m.Content)
			}
			sawIndex = true
		}
	}
	if !sawIndex {
		t.Fatalf("no skill-index system message in provider messages: %+v", msgs)
	}
}

// TestRunAgentNoIndexWhenNoModelVisibleSkills guards the nil-index case: a
// catalog with no model-visible skills must render to a nil index so the engine
// omits the system block and preserves the no-index wire bytes.
func TestRunAgentNoIndexWhenNoModelVisibleSkills(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	skills := discoverSkills(t.TempDir(), ws)
	reg, _ := tools.NewRegistry(tools.Deps{TempHost: t.TempDir(), Runner: tools.RealRunner, Workspace: ws, Skills: skills})

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", skills, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	for _, m := range cap.reqs[0].Messages {
		if skillindex.IsBlock(m.Content) {
			t.Fatalf("skill-index system message present with no model-visible skills:\n%s", m.Content)
		}
	}
}

// TestRunAgentNoIndexWhenCatalogNil guards the nil-catalog half of the nil-index
// contract: a nil catalog must not render (RenderIndex would dereference a nil
// receiver), so runAgent leaves the index nil and the engine omits the block.
func TestRunAgentNoIndexWhenCatalogNil(t *testing.T) {
	reg, _ := tools.NewRegistry(tools.Deps{TempHost: t.TempDir(), Runner: tools.RealRunner, Workspace: t.TempDir()})

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", nil, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	for _, m := range cap.reqs[0].Messages {
		if skillindex.IsBlock(m.Content) {
			t.Fatalf("skill-index system message present with a nil catalog:\n%s", m.Content)
		}
	}
}

// writeAppSkill writes a minimal model-visible skill pack under a workspace.
func writeAppSkill(t *testing.T, ws, name, desc string) {
	t.Helper()
	dir := filepath.Join(ws, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	md := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// TestRunAgentDynamicSkillCatalogGatesInjection guards the retrieval switch: with
// the setting on, only the selected subset (plus a "more exist" note) is
// injected; with it off (the default), the full catalog is injected.
func TestRunAgentDynamicSkillCatalogGatesInjection(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	descs := map[string]string{
		"code-review":        "review a branch or PR against the standards and the spec",
		"clean-code":         "turn working code into clean code",
		"tdd":                "build test-first, red green refactor",
		"diagnosing-bugs":    "diagnose hard bugs with a hypothesis loop",
		"domain-modeling":    "build and sharpen the domain model",
		"research":           "investigate against primary sources",
		"grilling":           "grill a plan relentlessly",
		"codebase-design":    "design deeper module interfaces",
		"writing-for-agents": "write documents for agents",
		"subagents":          "outsource work to a batch subagent",
		"perf":               "profile a hot path and cut its cost",
		"docs":               "refresh user-facing documentation",
		"refactor":           "refactor a module without changing behaviour",
		"validate":           "validate a release package",
	}
	for name, desc := range descs {
		writeAppSkill(t, ws, name, desc)
	}
	skills := discoverSkills(t.TempDir(), ws)
	reg, _ := tools.NewRegistry(tools.Deps{TempHost: t.TempDir(), Runner: tools.RealRunner, Workspace: ws, Skills: skills})

	const prompt = "please review my changes on this branch"

	// Default: full catalog.
	full := runAndCaptureSkillIndex(t, reg, skills, prompt, false)
	if !strings.Contains(full, "clean-code:") || !strings.Contains(full, "subagents:") {
		t.Fatalf("full-catalog default omitted a skill:\n%s", full)
	}
	if strings.Contains(full, "more skill(s) exist") {
		t.Fatalf("full catalog should carry no 'more exist' note:\n%s", full)
	}

	// Opt-in: subset plus the note.
	sub := runAndCaptureSkillIndex(t, reg, skills, prompt, true)
	if !strings.Contains(sub, "code-review:") {
		t.Fatalf("dynamic subset omitted the relevant skill:\n%s", sub)
	}
	if !strings.Contains(sub, "more skill(s) exist") {
		t.Fatalf("dynamic subset did not announce omitted skills:\n%s", sub)
	}
	if strings.Contains(sub, "writing-for-agents:") {
		t.Fatalf("dynamic subset leaked an irrelevant skill:\n%s", sub)
	}
}

// runAndCaptureSkillIndex runs one turn and returns the injected skill-index
// system message, or fails when none was injected.
func runAndCaptureSkillIndex(t *testing.T, reg *tools.Registry, catalog *tools.Catalog, prompt string, dynamic bool) string {
	t.Helper()
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})
	cfg := config.Default()
	cfg.DynamicSkillCatalog = dynamic
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), prompt, catalog, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	for _, m := range cap.reqs[0].Messages {
		if m.Role == provider.RoleSystem && skillindex.IsBlock(m.Content) {
			return m.Content
		}
	}
	t.Fatalf("no skill-index system message injected; messages: %+v", cap.reqs[0].Messages)
	return ""
}
