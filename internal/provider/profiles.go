// Package provider describes LLM provider behavior behind Eitri config IDs.
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Descriptor exposes caller-safe provider metadata for config/UI decisions.
type Descriptor struct {
	ID                  string
	DisplayName         string
	DefaultBaseURL      string
	APIKeyRequired      bool
	CredentialName      string
	SupportsPromptCache bool // provider supports prompt_cache_key on chat requests
}

// SupportedThinkingLevels returns the thinking levels a model supports,
// or nil if the model/provider doesn't support thinking level control.
// Most reasoning models (deepseek*, o1, o3, etc.) support "low", "medium", "high".
func SupportedThinkingLevels(providerID, modelName string) []string {
	model := strings.ToLower(modelName)
	if _, after, ok := strings.Cut(model, "/"); ok {
		model = after
	}
	// OpenAI-compatible reasoning models
	if strings.HasPrefix(model, "gpt-5") ||
		strings.HasPrefix(model, "deepseek") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.Contains(model, "reasoning") {
		return []string{"low", "medium", "high"}
	}
	// Anthropic-compatible models (via OpenCode Go route)
	if strings.HasPrefix(model, "qwen") || strings.HasPrefix(model, "minimax") {
		return []string{"low", "medium", "high"}
	}
	return nil
}

// profile captures provider-internal URLs, credential policy, model discovery,
// and request headers used by Eitri's OpenAI-style transport.
type modelListResult struct {
	IDs            []string
	ContextWindows map[string]int
	ModelAPIs      map[string]string
}

type profile struct {
	Descriptor
	modelListPath  string
	chatPath       string
	stripV1Suffix  bool
	applyHeaders   func(*http.Request, string)
	parseModelList func(io.Reader) (modelListResult, error)
	authHandler    authHandler
}

// ModelListURL returns absolute model discovery URL for baseURL.
func (p profile) ModelListURL(baseURL string) string {
	return p.join(baseURL, p.modelListPath)
}

// ChatCompletionsURL returns absolute chat-completions URL for baseURL.
func (p profile) ChatCompletionsURL(baseURL string) string {
	return p.join(baseURL, p.chatPath)
}

// ApplyHeaders applies provider headers common to model discovery and chat.
func (p profile) ApplyHeaders(req *http.Request, apiKey string) {
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if p.applyHeaders != nil {
		p.applyHeaders(req, apiKey)
	}
}

// RequiredCredentialName returns user-facing credential name for validation errors.
func (p profile) RequiredCredentialName() string {
	if p.CredentialName != "" {
		return p.CredentialName
	}
	return "api_key"
}

// ParseModelList parses provider model discovery response into selectable IDs,
// optional per-model context windows, and optional per-model API selection.
func (p profile) ParseModelList(r io.Reader) (modelListResult, error) {
	return p.parseModelList(r)
}

func (p profile) join(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if p.stripV1Suffix {
		base = strings.TrimSuffix(base, "/v1")
	}
	return base + path
}

var profiles = map[string]profile{
	"opencode_go": {
		Descriptor: Descriptor{
			ID:                  "opencode_go",
			DisplayName:         "OpenCode Go",
			DefaultBaseURL:      "https://opencode.ai/zen/go/v1",
			APIKeyRequired:      true,
			SupportsPromptCache: true,
		},
		modelListPath:  "/v1/models",
		chatPath:       "/v1/chat/completions",
		stripV1Suffix:  true,
		parseModelList: parseOpenAIModelList,
	},
	"custom_openai": {
		Descriptor: Descriptor{
			ID:                  "custom_openai",
			DisplayName:         "Custom OpenAI (advanced/best-effort)",
			DefaultBaseURL:      "",
			APIKeyRequired:      false,
			SupportsPromptCache: true,
		},
		modelListPath:  "/v1/models",
		chatPath:       "/v1/chat/completions",
		stripV1Suffix:  true,
		parseModelList: parseOpenAIModelList,
	},
	"github_copilot": {
		Descriptor: Descriptor{
			ID:             "github_copilot",
			DisplayName:    "GitHub Copilot",
			DefaultBaseURL: "https://api.githubcopilot.com",
			APIKeyRequired: true,
			CredentialName: "token",
		},
		modelListPath:  "/models",
		chatPath:       "/chat/completions",
		applyHeaders:   applyGitHubCopilotHeaders,
		parseModelList: parseGitHubCopilotModelList,
		authHandler:    githubCopilotAuthHandler{},
	},
}

// Describe returns caller-safe provider metadata by config provider ID.
func Describe(id string) (Descriptor, error) {
	p, err := getProfile(id)
	if err != nil {
		return Descriptor{}, err
	}
	return p.Descriptor, nil
}

// MustDescribe returns caller-safe provider metadata and panics if id is unsupported.
func MustDescribe(id string) Descriptor {
	d, err := Describe(id)
	if err != nil {
		panic(err)
	}
	return d
}

func getProfile(id string) (profile, error) {
	p, ok := profiles[id]
	if !ok {
		return profile{}, fmt.Errorf("unsupported provider %q", id)
	}
	return p, nil
}

// IDs returns supported provider IDs.
func IDs() []string {
	return []string{"opencode_go", "custom_openai", "github_copilot"}
}

func parseOpenAIModelList(r io.Reader) (modelListResult, error) {
	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&modelsResp); err != nil {
		return modelListResult{}, fmt.Errorf("failed to parse model list: %w", err)
	}

	modelIDs := make([]string, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		if m.ID != "" {
			modelIDs = append(modelIDs, m.ID)
		}
	}
	return modelListResult{IDs: modelIDs}, nil
}

const (
	gitHubCopilotUserAgent     = "GithubCopilot/1.100.0"
	gitHubCopilotEditorVersion = "vscode/1.80.0"
)

func gitHubCopilotExtraHeaders() map[string]string {
	return map[string]string{
		"Editor-Version":       gitHubCopilotEditorVersion,
		"X-GitHub-Api-Version": "2026-06-01",
		"Openai-Intent":        "conversation-panel",
		"x-initiator":          "user",
	}
}

func applyGitHubCopilotHeaders(req *http.Request, _ string) {
	// Copilot API expects headers matching the official VSCode extension.
	req.Header.Set("User-Agent", gitHubCopilotUserAgent)
	for name, value := range gitHubCopilotExtraHeaders() {
		req.Header.Set(name, value)
	}
}

type githubCopilotModel struct {
	ID     string `json:"id"`
	Policy struct {
		State string `json:"state"`
	} `json:"policy"`
	ModelPickerEnabled bool     `json:"model_picker_enabled"`
	SupportedEndpoints []string `json:"supported_endpoints"`
	MaxInputTokens     int      `json:"max_input_tokens,omitempty"`
}

const (
	GitHubCopilotAPIChat      = "chat"
	GitHubCopilotAPIResponses = "responses"
)

func parseGitHubCopilotModelList(r io.Reader) (modelListResult, error) {
	var modelsResp struct {
		Data   []githubCopilotModel `json:"data"`
		Models []githubCopilotModel `json:"models"`
	}
	if err := json.NewDecoder(r).Decode(&modelsResp); err != nil {
		return modelListResult{}, fmt.Errorf("failed to parse model list: %w", err)
	}

	models := modelsResp.Data
	if len(models) == 0 {
		models = modelsResp.Models
	}

	modelIDs := make([]string, 0, len(models))
	contextWindows := make(map[string]int, len(models))
	modelAPIs := make(map[string]string, len(models))
	for _, m := range models {
		api, ok := gitHubCopilotModelAPI(m.SupportedEndpoints)
		if m.ID == "" || m.Policy.State == "disabled" || !m.ModelPickerEnabled || !ok {
			continue
		}
		modelIDs = append(modelIDs, m.ID)
		modelAPIs[m.ID] = api
		if m.MaxInputTokens > 0 {
			contextWindows[m.ID] = m.MaxInputTokens
		}
	}
	return modelListResult{IDs: modelIDs, ContextWindows: contextWindows, ModelAPIs: modelAPIs}, nil
}

func gitHubCopilotModelAPI(endpoints []string) (string, bool) {
	if supportsEndpoint(endpoints, "/chat/completions") {
		return GitHubCopilotAPIChat, true
	}
	if supportsEndpoint(endpoints, "/responses") {
		return GitHubCopilotAPIResponses, true
	}
	return "", false
}

func supportsEndpoint(endpoints []string, want string) bool {
	for _, endpoint := range endpoints {
		if endpoint == want {
			return true
		}
	}
	return false
}
