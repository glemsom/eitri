package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIDiscoversModels verifies the OpenAI-compatible client can list the
// available models from the provider's /models endpoint (model discovery,
// T12). The base URL is derived from the Chat-Completions
// endpoint by stripping the /chat/completions suffix.
func TestOpenAIDiscoversModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4"},{"id":"grok-2"}]}`))
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("k", srv.URL+"/v1/chat/completions")
	models, err := cl.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v, want nil", err)
	}
	if len(models) != 3 || models[0] != "deepseek-v4-flash" {
		t.Fatalf("Models() = %v, want [deepseek-v4-flash deepseek-v4 grok-2]", models)
	}
}

// TestOpenAIDiscoverModelsCarriesAuth verifies model discovery uses the same
// Bearer credential as streaming.
func TestOpenAIDiscoverModelsCarriesAuth(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	cl := NewOpenAICompatible("disc-key", srv.URL+"/v1/chat/completions")
	if _, err := cl.Models(context.Background()); err != nil {
		t.Fatalf("Models() error = %v, want nil", err)
	}
	if sawAuth != "Bearer disc-key" {
		t.Fatalf("Models() Authorization = %q, want Bearer disc-key", sawAuth)
	}
}

// TestFakeDiscoversModels verifies the fake provider stands in for model
// discovery at the engine/app seam: it surfaces a committed model list so
// discovery is exercisable without a network.
func TestFakeDiscoversModels(t *testing.T) {
	models, err := NewFake("../provider/testdata/hello.sse").Models(context.Background())
	if err != nil {
		t.Fatalf("Fake.Models() error = %v, want nil", err)
	}
	if len(models) == 0 {
		t.Fatalf("Fake.Models() = empty, want the fixture model list")
	}
	want := "deepseek-v4-flash"
	found := false
	for _, m := range models {
		if m == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Fake.Models() = %v, want it to include %q", models, want)
	}
}

// TestScriptedDoesNotListModels asserts Scripted (and other minimal providers)
// simply don't expose the optional ModelLister capability; callers type-assert
// and treat absence as "no discovery" rather than erroring.
func TestScriptedDoesNotListModels(t *testing.T) {
	sp := NewScripted(func(_ context.Context, _ Request) (Stream, error) {
		return StreamFunc(Chunk{FinishReason: "stop", Done: true}), nil
	})
	if _, ok := any(sp).(ModelLister); ok {
		t.Fatal("Scripted should not implement ModelLister")
	}
}
