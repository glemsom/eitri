package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/provider"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tool"
)

// buildSystemPrompt assembles the full system prompt from the active persona
// (or the generic persona / built-in fallback), repository instructions,
// skills catalog, and skill activations — including skills required by the
// persona.
//
// Persona resolution:
//  1. cfg.ActivePersona — a healthy persona's SystemPrompt wins; its required
//     skills feed the <required_skills> directive.
//  2. The generic persona — when no persona is active, or the active persona is
//     missing/corrupt. The generic persona's prompt is the "settings prompt":
//     the Settings UI's Prompt field mirrors into
//     ~/.eitri/personas/generic.yaml, and it is resolved from disk so broken
//     active personas fall back to it (not a bare built-in constant).
//  3. cfg.SystemPrompt then persona.DefaultPrompt — legacy settings value and
//     built-in defaults when the generic persona file is unavailable/empty.
//
// cfg.SystemPrompt is NOT a top-precedence override: a healthy active persona
// always wins over it, matching the semantics that the settings prompt edits
// the generic persona instead of shadowing every persona.
//
// Manually activated skills (loaded by the agent via skill()) are injected
// with their full content under the "Activated skill" label.
// Persona-required skills are NOT pre-injected; they are listed as a
// startup directive instructing the agent to call skill() for each one,
// establishing commitment through the tool-call result.
func buildSystemPrompt(cfg RunConfig, skillCtx sessionSkillContext, skillsSvc *skills.Service) (string, error) {
	systemPrompt, personaRequiredSkills := resolveBasePrompt(cfg)

	var fullSystemPrompt strings.Builder
	fullSystemPrompt.WriteString(systemPrompt)

	repoInstructions, err := readRepositoryInstructions(cfg.Workspace)
	if err != nil {
		return "", fmt.Errorf("read repository instructions: %w", err)
	}
	if repoInstructions != "" {
		fullSystemPrompt.WriteString("\n\n" + repoInstructions)
	}

	if skillsSvc != nil {
		catalog := skillsSvc.SkillsCatalogXML()
		if catalog != "" {
			fullSystemPrompt.WriteString("\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context. After loading a skill, check its instructions for references to other skills — if any are mentioned, load them too.")
		}
	}

	// Build a set of persona-required skill names for quick lookup.
	personaRequiredSet := make(map[string]struct{}, len(personaRequiredSkills))
	for _, name := range personaRequiredSkills {
		personaRequiredSet[name] = struct{}{}
	}

	// Separate manually activated skills (loaded by the agent via skill())
	// from persona-required skills. Persona-required skills are NOT
	// pre-injected with content; instead they are listed as a directive
	// below so the agent calls skill() to load each one on its first turn.
	var manuallyActivated []runSkillActivation
	var personaRequired []string
	seen := make(map[string]struct{}, len(skillCtx.Activations))
	for _, a := range skillCtx.Activations {
		if _, ok := personaRequiredSet[a.Name]; ok {
			// Persona-required — track for directive, don't inject content
			personaRequired = append(personaRequired, a.Name)
		} else {
			manuallyActivated = append(manuallyActivated, a)
		}
		seen[a.Name] = struct{}{}
	}

	// Add persona-required skills that aren't yet in the session activations.
	for _, skillName := range personaRequiredSkills {
		if _, ok := seen[skillName]; ok {
			continue // already tracked above
		}
		if skillsSvc == nil {
			continue
		}
		skill := skillsSvc.Lookup(skillName)
		if skill == nil {
			slog.Warn("Persona-required skill not found, skipping", "skill", skillName, "persona", cfg.ActivePersona)
			continue
		}
		personaRequired = append(personaRequired, skillName)
		seen[skillName] = struct{}{}
	}

	// Add manually activated skill content.
	for _, activation := range manuallyActivated {
		fullSystemPrompt.WriteString("\n\nActivated skill \"" + activation.Name + "\":\n" + activation.Content)
	}

	// Add directive for persona-required skills so the agent loads them.
	// The <required_skills> XML block mirrors the <repository_instructions> pattern,
	// giving the directive strong visual separation and making it harder for the
	// agent to overlook.
	if len(personaRequired) > 0 {
		fullSystemPrompt.WriteString("\n\n<required_skills>\nRequired skills for this persona: " + strings.Join(personaRequired, ", ") + ".\nOn your first turn, call skill(\"name\") for each required skill above to load its instructions, references, and scripts into context.\n</required_skills>")
	}

	return fullSystemPrompt.String(), nil
}

// resolveBasePrompt resolves the base (pre-appendix) system prompt and any
// persona-required skills.
//
// A healthy active persona's prompt wins. When no persona is active, or the
// active persona file is missing/corrupt (broken-persona fallback), the
// generic persona is resolved from disk — its prompt is the "settings prompt"
// mirrored to ~/.eitri/personas/generic.yaml — so the config's system_prompt
// override is honoured consistently instead of being bypassed by a bare
// built-in constant.
func resolveBasePrompt(cfg RunConfig) (systemPrompt string, requiredSkills []string) {
	// A healthy active persona's prompt wins over everything (including any
	// settings prompt).
	if cfg.ActivePersona != "" {
		def, err := persona.LoadWithHome(cfg.Workspace, resolveHomeDir(cfg.HomeDir), cfg.ActivePersona)
		if err != nil {
			// Persona file missing or unreadable — warn and fall back to generic.
			// This handles the case where active_persona was set in config but the
			// file was deleted later (e.g. UI mode) or is corrupt.
			slog.Warn("persona not found, falling back to generic",
				slog.String("persona", cfg.ActivePersona),
				slog.Any("error", err),
			)
		} else {
			if def.SystemPrompt != "" {
				return def.SystemPrompt, def.RequiredSkills
			}
			// Healthy persona with an empty prompt: use the built-in default but
			// keep its required skills.
			return persona.DefaultPrompt, def.RequiredSkills
		}
	}

	// No valid active persona — resolve the generic persona from disk. Its
	// prompt is the settings prompt; if unavailable (file missing/corrupt and
	// no legacy config value), fall back to the built-in default.
	def, err := persona.LoadWithHome(cfg.Workspace, resolveHomeDir(cfg.HomeDir), persona.GenericName)
	var genericSkills []string
	if err == nil {
		genericSkills = def.RequiredSkills
		if def.SystemPrompt != "" {
			return def.SystemPrompt, def.RequiredSkills
		}
	}
	if cfg.SystemPrompt != "" {
		return cfg.SystemPrompt, genericSkills
	}
	return persona.DefaultPrompt, genericSkills
}

// buildLLMService resolves provider authentication, constructs an LLM service,
// builds the base tool registry, and assembles the system prompt.
// If debugRecorder is non-nil and sessionID is non-empty, the service's HTTP
// transport is wrapped for request/response recording.
func buildLLMService(ctx context.Context, cfg RunConfig, sessionID string, debugRecorder *debug.Recorder, persistAuth provider.PersistAuthFunc, skillDirs []string, skillsSvc *skills.Service, uiSessionMgr *uisession.Manager, skillCtx sessionSkillContext) (*litellm.Client, *tool.Registry, string, error) {
	reqAuth := provider.ResolveAuthRequest{
		ProviderID:   cfg.ProviderID,
		APIKey:       cfg.APIKey,
		ProviderAuth: cfg.ProviderAuth,
	}
	resolvedKey, _, err := provider.ResolveAuth(ctx, reqAuth, persistAuth)
	if err != nil {
		return nil, nil, "", fmt.Errorf("auth resolution: %w", err)
	}
	apiKey := cfg.APIKey
	if resolvedKey != "" {
		apiKey = resolvedKey
	}

	litellmCfg := provider.LitellmConfig{
		ProviderID:   cfg.ProviderID,
		Model:        cfg.ModelName,
		ModelAPI:     resolveModelAPI(ctx, cfg, persistAuth),
		BaseURL:      cfg.BaseURL,
		APIKey:       apiKey,
		DebugPrompt:  cfg.DebugPrompt,
		DebugRequest: cfg.DebugRequest,
		DebugLLMDir:  cfg.DebugLLMDir,
	}

	if debugRecorder != nil && sessionID != "" {
		litellmCfg.RoundTripper = debug.NewRecordingRoundTripper(nil, debugRecorder, sessionID, cfg.ProviderID)
	}

	client, err := provider.NewLitellmClient(litellmCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create LLM service: %w", err)
	}

	toolReg := buildBaseToolRegistry(cfg, skillDirs, skillsSvc, uiSessionMgr)

	fullSystemPrompt, err := buildSystemPrompt(cfg, skillCtx, skillsSvc)
	if err != nil {
		return nil, nil, "", fmt.Errorf("build system prompt: %w", err)
	}

	return client, toolReg, fullSystemPrompt, nil
}
