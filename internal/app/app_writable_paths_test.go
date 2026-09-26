package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

// TestRunAgentCarriesWritablePaths guards the batch/TUI shared seam: whatever
// the registry binds read-write must reach the model as a per-run directive, so
// both surfaces state the same boundary the sandbox enforces.
func TestRunAgentCarriesWritablePaths(t *testing.T) {
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true}), nil
	}), mockTranscript{})

	workspace := t.TempDir()
	extra := filepath.Join(t.TempDir(), "kube")
	reg, err := tools.NewRegistry(tools.Deps{
		Workspace:     workspace,
		TempHost:      filepath.Join(t.TempDir(), "tmp"),
		ExtraWritable: []string{extra},
		Runner:        tools.RealRunner,
	})
	if err != nil {
		t.Fatalf("NewRegistry error = %v", err)
	}

	if _, err := runAgent(context.Background(), e, config.Default(), reg, "sess-"+t.Name(), "hi", &tools.Catalog{}, nil, nil); err != nil {
		t.Fatalf("runAgent error = %v, want nil", err)
	}
	if len(cap.reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	directive := cap.reqs[0].Messages[1]
	if !strings.Contains(directive.Content, "## Write permissions") {
		t.Fatalf("workspace directive missing the write-permissions section: %q", directive.Content)
	}
	for _, p := range []string{workspace, extra, reg.TempHost()} {
		if !strings.Contains(directive.Content, p) {
			t.Errorf("directive omits writable path %q: %q", p, directive.Content)
		}
	}
}
