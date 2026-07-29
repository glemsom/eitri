package templates

import "github.com/glemsom/eitri/internal/provider"

// CopilotDeviceFlowView renders GitHub Copilot device-flow status inside Settings.
type CopilotDeviceFlowView struct {
	ID              string
	UserCode        string
	VerificationURI string
	PollURL         string
	PollTrigger     string
	CancelURL       string
}

func baseURLStatus(providerID, baseURL string) string {
	desc, err := provider.Describe(providerID)
	if err != nil {
		return "Provider endpoint"
	}
	if baseURL == "" {
		if providerID == "custom_openai" {
			return "Endpoint required for Custom OpenAI"
		}
		return "Endpoint required"
	}
	if desc.DefaultBaseURL != "" && baseURL == desc.DefaultBaseURL {
		return "Using provider default endpoint"
	}
	if desc.DefaultBaseURL != "" {
		return "Using endpoint override"
	}
	return "Using custom endpoint"
}
