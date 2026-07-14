package provider_test

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestOpenAICompatibleProfilesBuildModelAndChatURLs(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"opencode_go", "custom_openai"} {
		prof, err := provider.Get(providerID)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", providerID, err)
		}

		if got := prof.ModelListURL("https://example.test/v1/"); got != "https://example.test/v1/models" {
			t.Errorf("%s ModelListURL = %q, want %q", providerID, got, "https://example.test/v1/models")
		}
		if got := prof.ChatCompletionsURL("https://example.test/v1/"); got != "https://example.test/v1/chat/completions" {
			t.Errorf("%s ChatCompletionsURL = %q, want %q", providerID, got, "https://example.test/v1/chat/completions")
		}
	}
}

func TestOpenAICompatibleProfilesParseOpenAIModelList(t *testing.T) {
	t.Parallel()

	prof, err := provider.Get("custom_openai")
	if err != nil {
		t.Fatal(err)
	}

	models, err := prof.ParseModelList(strings.NewReader(`{"object":"list","data":[{"id":"gpt-4"},{"id":""},{"id":"gpt-3.5-turbo"}]}`))
	if err != nil {
		t.Fatalf("ParseModelList error: %v", err)
	}

	want := []string{"gpt-4", "gpt-3.5-turbo"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, models[i], want[i])
		}
	}
}

func TestProfilesKeepExistingAPIKeyRequirements(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"opencode_go":   true,
		"custom_openai": false,
	}
	for providerID, wantRequired := range cases {
		prof, err := provider.Get(providerID)
		if err != nil {
			t.Fatalf("Get(%q) error: %v", providerID, err)
		}
		if prof.APIKeyRequired != wantRequired {
			t.Errorf("%s APIKeyRequired = %v, want %v", providerID, prof.APIKeyRequired, wantRequired)
		}
	}
}
