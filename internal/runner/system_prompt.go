package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tool"
)

// buildSystemPrompt assembles the full system prompt from the active persona
// (or user override or default), repository instructions, skills catalog,
// and skill activations — including skills required by the persona.
//
// Precedence:
//  1. cfg.SystemPrompt (user override) — if non-empty, used directly
//  2. cfg.ActivePersona — resolved from disk, its SystemPrompt used as base
//  3. history.DefaultSystemPrompt — built-in fallback
//
// Manually activated skills (loaded by the agent via skill()) are injected
// with their full content under the "Activated skill" label.
// Persona-required skills are NOT pre-injected; they are listed as a
// startup directive instructing the agent to call skill() for each one,
// establishing commitment through the tool-call result.
func buildSystemPrompt(cfg runconfig.RunConfig, skillCtx sessionSkillContext, skillsSvc *skills.Service) (string, error) {
	systemPrompt := cfg.SystemPrompt
	var personaRequiredSkills []string
	if systemPrompt == "" {
		// No user override; try active persona.
		if cfg.ActivePersona != "" {
			def, err := persona.Load(cfg.Workspace, cfg.ActivePersona)
			if err != nil {
				// Persona file missing or unreadable — warn and fall back to default.
				// This handles the case where active_persona was set in config but the
				// file was deleted later (e.g. UI mode). Batch mode validates the persona
				// explicitly before calling StartRun/BatchRun.
				slog.Warn("persona not found, falling back to default",
					slog.String("persona", cfg.ActivePersona),
					slog.Any("error", err),
				)
			} else {
				if def.SystemPrompt != "" {
					systemPrompt = def.SystemPrompt
				}
				personaRequiredSkills = def.RequiredSkills
			}
		}
		// Fallback to built-in default.
		if systemPrompt == "" {
			systemPrompt = history.DefaultSystemPrompt
		}
	}

	fullSystemPrompt := systemPrompt

	repoInstructions, err := readRepositoryInstructions(cfg.Workspace)
	if err != nil {
		return "", fmt.Errorf("read repository instructions: %w", err)
	}
	if repoInstructions != "" {
		fullSystemPrompt += "\n\n" + repoInstructions
	}

	if skillsSvc != nil {
		catalog := skillsSvc.SkillsCatalogXML()
		if catalog != "" {
			fullSystemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context. After loading a skill, check its instructions for references to other skills — if any are mentioned, load them too."
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
		fullSystemPrompt += "\n\nActivated skill \"" + activation.Name + "\":\n" + activation.Content
	}

	// Add directive for persona-required skills so the agent loads them.
	// The <required_skills> XML block mirrors the <repository_instructions> pattern,
	// giving the directive strong visual separation and making it harder for the
	// agent to overlook.
	if len(personaRequired) > 0 {
		fullSystemPrompt += "\n\n<required_skills>\nRequired skills for this persona: " + strings.Join(personaRequired, ", ") + ".\nOn your first turn, call skill(\"name\") for each required skill above to load its instructions, references, and scripts into context.\n</required_skills>"
	}

	return fullSystemPrompt, nil
}

// buildLLMService resolves provider authentication, constructs an LLM service,
// builds the base tool registry, and assembles the system prompt.
// If debugRecorder is non-nil and sessionID is non-empty, the service's HTTP
// transport is wrapped for request/response recording.
func buildLLMService(ctx context.Context, cfg runconfig.RunConfig, sessionID string, debugRecorder *debug.Recorder, persistAuth provider.PersistAuthFunc, skillDirs []string, skillsSvc *skills.Service, uiSessionMgr *uisession.Manager, skillCtx sessionSkillContext) (llm.LLMService, *tool.Registry, string, error) {
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

	adapterCfg := llm.AdapterConfig{
		ProviderID:   cfg.ProviderID,
		Model:        cfg.ModelName,
		BaseURL:      cfg.BaseURL,
		APIKey:       apiKey,
		DebugPrompt:  cfg.DebugPrompt,
		DebugRequest: cfg.DebugRequest,
		DebugLLMDir:  cfg.DebugLLMDir,
	}

	if debugRecorder != nil && sessionID != "" {
		adapterCfg.RoundTripper = debug.NewRecordingRoundTripper(nil, debugRecorder, sessionID, cfg.ProviderID)
	}

	llmSvc, err := llm.NewLLMService(adapterCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create LLM service: %w", err)
	}

	toolReg := buildBaseToolRegistry(cfg, skillDirs, skillsSvc, uiSessionMgr)

	fullSystemPrompt, err := buildSystemPrompt(cfg, skillCtx, skillsSvc)
	if err != nil {
		return nil, nil, "", fmt.Errorf("build system prompt: %w", err)
	}

	return llmSvc, toolReg, fullSystemPrompt, nil
}
