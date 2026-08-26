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

	skills := discoverSkills(ws)
	reg := tools.NewRegistry(tools.Deps{Workspace: ws, Skills: skills, GUID: tools.GUID("idx-" + t.Name())})

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
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "<available_skills>") {
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

	skills := discoverSkills(ws)
	reg := tools.NewRegistry(tools.Deps{Workspace: ws, Skills: skills, GUID: tools.GUID("noidx-" + t.Name())})

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
		if strings.Contains(m.Content, "<available_skills>") {
			t.Fatalf("skill-index system message present with no model-visible skills:\n%s", m.Content)
		}
	}
}

// TestRunAgentNoIndexWhenCatalogNil guards the nil-catalog half of the nil-index
// contract: a nil catalog must not render (RenderIndex would dereference a nil
// receiver), so runAgent leaves the index nil and the engine omits the block.
func TestRunAgentNoIndexWhenCatalogNil(t *testing.T) {
	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir(), GUID: tools.GUID("nilcat-" + t.Name())})

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
		if strings.Contains(m.Content, "<available_skills>") {
			t.Fatalf("skill-index system message present with a nil catalog:\n%s", m.Content)
		}
	}
}
