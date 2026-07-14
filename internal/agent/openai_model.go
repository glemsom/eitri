package agent

import "github.com/glemsom/eitri/internal/provider"

// OpenAIModel aliases provider-owned OpenAI-compatible chat model.
type OpenAIModel = provider.OpenAIModel

// NewOpenAIModel creates an OpenAI-compatible model.LLM using OpenCode Go profile.
func NewOpenAIModel(name, baseURL, apiKey string) *OpenAIModel {
	return provider.NewOpenAIModel(name, baseURL, apiKey)
}

// NewOpenAIModelForProvider creates an OpenAI-style model.LLM for configured provider profile.
func NewOpenAIModelForProvider(name, baseURL, apiKey, providerID string) (*OpenAIModel, error) {
	return provider.NewOpenAIModelForProvider(name, baseURL, apiKey, providerID)
}
