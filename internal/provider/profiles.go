// Package provider describes LLM provider behavior behind Eitri config IDs.
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Profile captures provider-specific URLs, credential policy, model discovery,
// and request headers used by Eitri's OpenAI-style transport.
type Profile struct {
	ID             string
	DisplayName    string
	DefaultBaseURL string
	APIKeyRequired bool
	CredentialName string
	modelListPath  string
	chatPath       string
	stripV1Suffix  bool
	applyHeaders   func(*http.Request, string)
	parseModelList func(io.Reader) ([]string, error)
}

// ModelListURL returns the absolute model discovery URL for baseURL.
func (p Profile) ModelListURL(baseURL string) string {
	return p.join(baseURL, p.modelListPath)
}

// ChatCompletionsURL returns the absolute chat-completions URL for baseURL.
func (p Profile) ChatCompletionsURL(baseURL string) string {
	return p.join(baseURL, p.chatPath)
}

// ApplyHeaders applies provider headers common to model discovery and chat.
func (p Profile) ApplyHeaders(req *http.Request, apiKey string) {
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if p.applyHeaders != nil {
		p.applyHeaders(req, apiKey)
	}
}

// RequiredCredentialName returns user-facing credential name for validation errors.
func (p Profile) RequiredCredentialName() string {
	if p.CredentialName != "" {
		return p.CredentialName
	}
	return "api_key"
}

// ParseModelList parses provider model discovery response into selectable IDs.
func (p Profile) ParseModelList(r io.Reader) ([]string, error) {
	return p.parseModelList(r)
}

func (p Profile) join(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if p.stripV1Suffix {
		base = strings.TrimSuffix(base, "/v1")
	}
	return base + path
}

var profiles = map[string]Profile{
	"opencode_go": {
		ID:             "opencode_go",
		DisplayName:    "OpenCode Go",
		DefaultBaseURL: "https://opencode.ai/zen/go",
		APIKeyRequired: true,
		modelListPath:  "/v1/models",
		chatPath:       "/v1/chat/completions",
		stripV1Suffix:  true,
		parseModelList: parseOpenAIModelList,
	},
	"custom_openai": {
		ID:             "custom_openai",
		DisplayName:    "Custom OpenAI (advanced/best-effort)",
		DefaultBaseURL: "",
		APIKeyRequired: false,
		modelListPath:  "/v1/models",
		chatPath:       "/v1/chat/completions",
		stripV1Suffix:  true,
		parseModelList: parseOpenAIModelList,
	},
	"github_copilot": {
		ID:             "github_copilot",
		DisplayName:    "GitHub Copilot",
		DefaultBaseURL: "https://api.githubcopilot.com",
		APIKeyRequired: true,
		CredentialName: "token",
		modelListPath:  "/models",
		chatPath:       "/chat/completions",
		applyHeaders:   applyGitHubCopilotHeaders,
		parseModelList: parseGitHubCopilotModelList,
	},
}

// Get returns a provider profile by config provider ID.
func Get(id string) (Profile, error) {
	p, ok := profiles[id]
	if !ok {
		return Profile{}, fmt.Errorf("unsupported provider %q", id)
	}
	return p, nil
}

// MustGet returns a provider profile and panics if id is unsupported.
func MustGet(id string) Profile {
	p, err := Get(id)
	if err != nil {
		panic(err)
	}
	return p
}

// IDs returns supported provider IDs.
func IDs() []string {
	return []string{"opencode_go", "custom_openai", "github_copilot"}
}

func parseOpenAIModelList(r io.Reader) ([]string, error) {
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse model list: %w", err)
	}

	modelIDs := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		if m.ID != "" {
			modelIDs = append(modelIDs, m.ID)
		}
	}
	return modelIDs, nil
}

func applyGitHubCopilotHeaders(req *http.Request, _ string) {
	req.Header.Set("User-Agent", "Eitri")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Openai-Intent", "conversation-panel")
	req.Header.Set("x-initiator", "user")
}

type githubCopilotModel struct {
	ID     string `json:"id"`
	Policy struct {
		State string `json:"state"`
	} `json:"policy"`
	ModelPickerEnabled bool     `json:"model_picker_enabled"`
	SupportedEndpoints []string `json:"supported_endpoints"`
}

func parseGitHubCopilotModelList(r io.Reader) ([]string, error) {
	var modelsResp struct {
		Data   []githubCopilotModel `json:"data"`
		Models []githubCopilotModel `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse model list: %w", err)
	}

	models := modelsResp.Data
	if len(models) == 0 {
		models = modelsResp.Models
	}

	modelIDs := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID == "" || m.Policy.State == "disabled" || !m.ModelPickerEnabled || !supportsEndpoint(m.SupportedEndpoints, "/chat/completions") {
			continue
		}
		modelIDs = append(modelIDs, m.ID)
	}
	return modelIDs, nil
}

func supportsEndpoint(endpoints []string, want string) bool {
	for _, endpoint := range endpoints {
		if endpoint == want {
			return true
		}
	}
	return false
}
