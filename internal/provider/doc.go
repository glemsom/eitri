// Package provider owns LLM provider profile definitions, authentication
// handling, and model discovery.
//
// It manages the static provider registry (profiles map), credential
// resolution (including GitHub Copilot OAuth device flow + token refresh),
// and model-list fetching from provider API endpoints.
//
// # Key types
//
//   - Descriptor — caller-safe provider metadata (name, base URL, auth policy)
//   - ResolvedAuth — normalized credential material ready for HTTP requests
//   - GitHubCopilotAuthState — persisted Copilot OAuth token state (access,
//     refresh, expiry)
//   - GitHubCopilotOAuthConfig — OAuth device-flow endpoint configuration
//   - GitHubDeviceCodeResponse — device-flow start response (user_code,
//     verification_uri, etc.)
//   - DiscoveryRequest / DiscoveryResult — model discovery input/output
//   - DiscoveryOptions — transport, refresh, and persistence config for discovery
//   - ResolveAuthRequest — input for credential resolution without discovery
//   - AuthUpdate — refreshed auth state returned for caller persistence
//   - PersistAuthFunc — callback signature for persisting refreshed credentials
//   - ResolveAuthOptions — transport and refresh config for auth resolution
//   - GitHubCopilotDeviceFlowPollStatus — poll outcome enum
//   - GitHubCopilotDeviceFlowPollResult — normalized poll outcome
//   - ValidateCredentials — checks provider config has usable credentials
//
// # Key functions
//
//   - Describe / MustDescribe — return Descriptor for a provider ID
//   - IDs — return all supported provider IDs
//   - SupportedThinkingLevels — return thinking levels for a model
//   - DiscoverModels — fetch available models from a provider endpoint
//   - ResolveAuth — resolve credentials with optional token refresh
//   - ValidateCredentials — quick credential sanity check
//   - StartGitHubCopilotDeviceFlow — initiate GitHub OAuth device flow
//   - PollGitHubCopilotDeviceFlow — poll device-flow status
//   - EncodeGitHubCopilotAuthState — marshal Copilot auth state for storage
//   - DefaultGitHubCopilotOAuthConfig — fill default OAuth endpoint values
//   - NormalizeConfigAuthState — canonicalize provider auth for config persistence
//
// # Dependencies
//
// stdlib only — no internal/eitri packages are imported.
//
// # Extension points
//
//  1. Add a new provider profile:
//     Add an entry to the profiles map in profiles.go with Descriptor,
//     modelListPath, chatPath, parseModelList, and optionally applyHeaders
//     and authHandler.
//
//  2. Add a new auth handler:
//     Implement the authHandler interface (auth.go) with Normalize and
//     Resolve methods, then assign it to the profile's authHandler field.
//
//  3. Add a new model-list parser:
//     Write a parseModelList func(io.Reader) ([]string, error) and assign
//     it to the profile's parseModelList field.
package provider
