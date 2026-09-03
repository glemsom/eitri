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

// TestRunAgentLoadsWorkspaceRootAgentsMd guards the batch/TUI shared seam: once
// runAgent sees a workspace-root AGENTS.md it must carry its content to the
// engine as a dedicated system-layer message, so repository-authored
// instructions reach the model without perturbing the byte-stable system
// prompt.
func TestRunAgentLoadsWorkspaceRootAgentsMd(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("# Repo guidance\n\nDo the thing.\n"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	reg := tools.NewRegistry(tools.Deps{Workspace: ws})

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", nil, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	msgs := cap.reqs[0].Messages
	var saw bool
	for _, m := range msgs {
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "## Repository instructions (AGENTS.md)") {
			if !strings.Contains(m.Content, "Do the thing.") {
				t.Fatalf("repo-instructions system message lacks the AGENTS.md content:\n%s", m.Content)
			}
			saw = true
		}
	}
	if !saw {
		t.Fatalf("no repo-instructions system message in provider messages: %+v", msgs)
	}
}

// TestRunAgentNoRepoInstructionsWithoutAgentsMd guards the absent-file case: a
// workspace without AGENTS.md must leave the wire messages untouched (no extra
// system message), preserving the pre-feature bytes.
func TestRunAgentNoRepoInstructionsWithoutAgentsMd(t *testing.T) {
	ws := t.TempDir() // deliberately no AGENTS.md
	reg := tools.NewRegistry(tools.Deps{Workspace: ws})

	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	cfg := config.Default()
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", nil, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	for _, m := range cap.reqs[0].Messages {
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "## Repository instructions") {
			t.Fatalf("repo-instructions system message present without a workspace AGENTS.md:\n%s", m.Content)
		}
	}
}
