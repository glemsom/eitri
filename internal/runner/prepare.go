package runner

import (
	"context"
	"log/slog"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/loop"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
)

// runPrep bundles the outputs of the shared parent-run preparation seam so UI
// and batch runs consume identical tool registries, LLM requests, and system
// prompts for the same config and prompt.
type runPrep struct {
	llmSvc       *litellm.Client
	toolReg      *tool.Registry
	req          *litellm.Request
	systemPrompt string
}

// runPrepOptions carries the genuinely mode-specific differences between a UI
// parent run, a batch parent run, and a delegated (sub-agent) run. Everything
// else in run preparation is shared by the seam.
type runPrepOptions struct {
	// sessionID is the run-scoped session identifier: the UI session ID for
	// browser runs, the batch session ID for headless runs, the task ID for
	// sub-agent runs. It keys the prompt cache, HTTP trace recording, and
	// EndSession cleanup.
	sessionID string
	// skillCtx carries the session's active skill activations. Batch and
	// sub-agent runs have no persisted activations and pass an empty context;
	// the system prompt still emits the skills catalog and the persona-required
	// <required_skills> directive.
	skillCtx sessionSkillContext
	// uiSessionMgr is nil for batch runs. When non-nil and allowDelegate is
	// true (i.e. a UI parent run), the UI-only render_quick_replies tool is
	// registered. Delegated runs never register it, even in a UI session.
	uiSessionMgr *uisession.Manager
	// allowDelegate gates the parent-agent tools. UI and batch parent runs set
	// it true, which registers delegate and collect (and render_quick_replies
	// when a UI session exists). A delegated (sub-agent) run sets it false and
	// gets only the leaf base toolset — bash, grep, read, write, edit,
	// render_mermaid_diagram, web_fetch, browser, and skill. Recursion is thus
	// gated on the seam: a leaf has no delegate tool, so it cannot recurse.
	allowDelegate bool
}

// prepareRun is the single run-preparation seam shared by the UI parent run
// (startRunWithConfig), the batch parent run (BatchRun), and delegated runs
// (SpawnSubAgent). For the same config and prompt it produces:
//
//   - the same tool registry. UI/batch parents get bash, grep, read, write,
//     edit, render_mermaid_diagram, web_fetch, browser, skill (when a skills
//     service is wired), plus delegate and collect; render_quick_replies is
//     registered only when a UI session exists. Delegated runs get only the
//     leaf base toolset — no delegate, no collect, and never
//     render_quick_replies.
//   - the same system prompt contract (skills catalog + <required_skills>
//     directive when the persona requires skills),
//   - the same LLM request behavior (max_output_tokens from config,
//     session-scoped prompt-cache key, thinking level).
//
// Callers own the mode-specific runtime wiring and cleanup: they must defer
// prep.toolReg.EndSession(opts.sessionID) and pass the service's crash-dump
// function to RunAgent so browser-tool connection release and loop-panic crash
// dumps behave identically.
func (s *RunService) prepareRun(ctx context.Context, cfg RunConfig, opts runPrepOptions) (runPrep, error) {
	llmSvc, toolReg, fullSystemPrompt, err := buildLLMService(ctx, cfg, opts.sessionID, s.debugRecorder, s.persistAuth, s.skillDirectories(), s.skillsSvc, opts.uiSessionMgr, opts.skillCtx)
	if err != nil {
		return runPrep{}, err
	}

	// Parent-agent tools shared by UI and batch parent runs. A delegated run
	// (allowDelegate false) is a leaf: it must not register delegate/collect,
	// so it cannot recurse. Recursion gating is a config value on this seam,
	// not an implicit toolset difference.
	if opts.allowDelegate {
		toolReg.Register(tool.NewDelegate(s))
		toolReg.Register(tool.NewCollect(s))
		// UI-only: quick-reply chips render into the browser DOM. Never on a
		// delegated run, even when the parent has a UI session.
		if opts.uiSessionMgr != nil {
			toolReg.Register(tool.NewRenderQuickReplies())
		}
	}

	return runPrep{
		llmSvc:       llmSvc,
		toolReg:      toolReg,
		req:          buildRunRequest(cfg, opts.sessionID),
		systemPrompt: fullSystemPrompt,
	}, nil
}

// buildRunRequest assembles the *litellm.Request shared by parent and
// sub-agent runs: the model, max_output_tokens from config, a session-scoped
// prompt-cache key when the provider supports it, and the thinking level.
func buildRunRequest(cfg RunConfig, sessionID string) *litellm.Request {
	req := &litellm.Request{
		Model: cfg.ModelName,
	}

	// Max output tokens per assistant turn. Config default is 32000 (generous
	// headroom for reasoning models whose thinking can otherwise exhaust a
	// small cap before emitting any tool call or answer). Zero means "no
	// explicit cap".
	if cfg.MaxOutputTokens > 0 {
		req.MaxTokens = &cfg.MaxOutputTokens
	}

	// Set session-scoped prompt cache key if the provider supports it.
	// Skip for Anthropic-routed models (qwen*, minimax*) because the
	// Anthropic provider rejects unknown provider options like prompt_cache_key.
	providerDesc, _ := provider.Describe(cfg.ProviderID)
	if providerDesc.SupportsPromptCache && !provider.IsAnthropicRoutedModel(cfg.ModelName) {
		if req.ProviderOptions == nil {
			req.ProviderOptions = make(litellm.ProviderOptions)
		}
		req.ProviderOptions["prompt_cache_key"] = sessionID
	}

	applyThinkingLevel(req, cfg)
	return req
}

// applyThinkingLevel sets the litellm thinking field when the configured model
// supports thinking levels, logging and skipping otherwise.
func applyThinkingLevel(req *litellm.Request, cfg RunConfig) {
	if cfg.ThinkingLevel == "" {
		return
	}
	if levels := provider.SupportedThinkingLevels(cfg.ProviderID, cfg.ModelName); len(levels) == 0 {
		slog.Info("model does not support thinking_level, skipping",
			slog.String("model", cfg.ModelName),
			slog.String("provider", cfg.ProviderID),
			slog.String("thinking_level", cfg.ThinkingLevel),
		)
	} else if !loop.IsReasoningModel(cfg.ModelName) {
		slog.Debug("model does not support litellm thinking field, skipping thinking_level",
			slog.String("model", cfg.ModelName),
			slog.String("thinking_level", cfg.ThinkingLevel),
		)
	} else {
		req.Thinking = &litellm.Thinking{
			Mode:   litellm.ThinkingEnabled,
			Effort: cfg.ThinkingLevel,
		}
	}
}
