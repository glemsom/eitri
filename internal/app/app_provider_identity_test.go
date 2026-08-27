package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
)

// TestRunAgentCarriesProviderIdentity guards the config-to-request seam: the
// provider family selected in config must reach the built Request so the
// shared dialect can apply provider-specific wire fields without guessing.
func TestRunAgentCarriesProviderIdentity(t *testing.T) {
	cap := &captureSkillRequests{}
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true}), nil
	}), mockTranscript{})

	reg := tools.NewRegistry(tools.Deps{Workspace: t.TempDir()})

	for _, tc := range []struct {
		cfgProvider string
		want        provider.ProviderID
	}{
		{cfgProvider: "opencode-go", want: provider.ProviderOpenCodeGo},
		{cfgProvider: "custom-openai", want: provider.ProviderCustomOpenAI},
	} {
		t.Run(tc.cfgProvider, func(t *testing.T) {
			cap.reqs = nil
			cfg := config.Default()
			cfg.Provider = tc.cfgProvider
			if _, err := runAgent(context.Background(), e, cfg, reg, "sess-"+t.Name(), "hi", &tools.Catalog{}, nil, nil); err != nil {
				t.Fatalf("runAgent error = %v, want nil", err)
			}
			if len(cap.reqs) == 0 {
				t.Fatal("provider received no requests")
			}
			if got := cap.reqs[0].ProviderID; got != tc.want {
				t.Fatalf("request ProviderID = %q, want %q", got, tc.want)
			}
		})
	}
}
