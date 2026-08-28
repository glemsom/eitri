package app

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

// TestRunAgentCarriesLynxDirective guards the boot-probe → runAgent seam: the
// per-run lynx flag must reach the engine as a dedicated system-layer
// HTML-rendering directive after the workspace directive, and must be absent
// when the probe reports lynx missing.
func TestRunAgentCarriesLynxDirective(t *testing.T) {
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})
	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir()})
	cfg := config.Default()

	// Lynx present: an HTML-rendering directive must ride the request.
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", &tools.Catalog{}, nil, nil, true); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	var sawLynx bool
	for _, m := range cap.reqs[0].Messages {
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "## HTML rendering") {
			sawLynx = true
		}
	}
	if !sawLynx {
		t.Fatalf("no HTML-rendering directive in provider messages: %+v", cap.reqs[0].Messages)
	}

	// Lynx absent: the same seam must stay byte-identical to the baseline.
	cap.reqs = nil
	if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", &tools.Catalog{}, nil, nil, false); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	for _, m := range cap.reqs[0].Messages {
		if strings.Contains(m.Content, "## HTML rendering") {
			t.Fatalf("HTML-rendering directive present with lynx unavailable: %s", m.Content)
		}
	}
}