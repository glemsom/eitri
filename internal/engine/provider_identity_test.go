package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestBuildCarriesProviderIdentityIntoRequest(t *testing.T) {
	t.Parallel()
	var got provider.ProviderID
	stream := provider.StreamFunc(provider.Chunk{Content: "ok"}, provider.Chunk{Done: true})
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		got = req.ProviderID
		return stream, nil
	}), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "hi", ProviderID: provider.ProviderOpenCodeGo}, AgentOptions{})
	if err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if got != provider.ProviderOpenCodeGo {
		t.Fatalf("request ProviderID = %q, want %q", got, provider.ProviderOpenCodeGo)
	}
}
