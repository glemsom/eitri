package templates

// CopilotDeviceFlowView renders GitHub Copilot device-flow status inside Settings.
type CopilotDeviceFlowView struct {
	ID              string
	UserCode        string
	VerificationURI string
	PollURL         string
	PollTrigger     string
	CancelURL       string
}
