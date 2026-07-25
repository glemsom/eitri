package runner

import (
	"context"
	"fmt"

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
// and skill activations.
//
// Precedence:
//  1. cfg.SystemPrompt (user override) — if non-empty, used directly
//  2. cfg.ActivePersona — resolved from disk, its SystemPrompt used as base
//  3. history.DefaultSystemPrompt — built-in fallback
func buildSystemPrompt(cfg runconfig.RunConfig, skillCtx sessionSkillContext, skillsSvc *skills.Service) (string, error) {
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		// No user override; try active persona.
		if cfg.ActivePersona != "" {
			def, err := persona.Load(cfg.Workspace, cfg.ActivePersona)
			if err == nil && def.SystemPrompt != "" {
				systemPrompt = def.SystemPrompt
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
			fullSystemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
		}
	}

	for _, activation := range skillCtx.Activations {
		fullSystemPrompt += "\n\nActivated skill \"" + activation.Name + "\":\n" + activation.Content
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
