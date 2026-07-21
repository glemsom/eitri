package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
)

// BatchRun runs a single prompt in headless batch mode.
// It uses requestHistoryManager (no UI sessions, no browser SSE),
// streams text tokens to the supplied io.Writer as they arrive,
// and blocks until the agent loop finishes or context is cancelled.
// Confirmation requests are denied (nil confirmer → error returned to LLM).
//
// Returns the final accumulated response text alongside any error.
func (s *RunService) BatchRun(ctx context.Context, prompt string, cfg RunConfig, out io.Writer) (string, error) {
	slog.Info("batch run starting",
		slog.String("model", cfg.ModelName),
		slog.String("provider", cfg.ProviderID),
	)

	// Validate config
	if cfg.BaseURL == "" || cfg.ModelName == "" {
		return "", fmt.Errorf("provider not configured: set base_url and model in settings")
	}

	// Build system prompt
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = history.DefaultSystemPrompt
	}

	fullSystemPrompt := systemPrompt

	// Add repository instructions if available
	repoInstructions, err := readRepositoryInstructions(cfg.Workspace)
	if err != nil {
		return "", fmt.Errorf("read repository instructions: %w", err)
	}
	if repoInstructions != "" {
		fullSystemPrompt += "\n\n" + repoInstructions
	}

	// Resolve auth
	reqAuth := provider.ResolveAuthRequest{
		ProviderID:   cfg.ProviderID,
		APIKey:       cfg.APIKey,
		ProviderAuth: cfg.ProviderAuth,
	}
	resolvedKey, _, authErr := provider.ResolveAuth(ctx, reqAuth, s.persistAuth)
	if authErr != nil {
		return "", fmt.Errorf("auth resolution: %w", authErr)
	}
	apiKey := cfg.APIKey
	if resolvedKey != "" {
		apiKey = resolvedKey
	}

	// Build LLM service
	llm, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: cfg.ProviderID,
		Model:      cfg.ModelName,
		BaseURL:    cfg.BaseURL,
		APIKey:     apiKey,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create LLM service: %w", err)
	}

	// Build tool registry (base tools only — no delegate/collect/quick_replies/skill)
	toolReg := buildBaseToolRegistry(cfg, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr)

	// Create request with system prompt and user message
	req := &litellm.Request{
		Model:  cfg.ModelName,
		Stream: true,
	}
	if cfg.ThinkingLevel != "" {
		req.ReasoningEffort = cfg.ThinkingLevel
	}
	req.Messages = []litellm.Message{
		{Role: "system", Content: fullSystemPrompt},
		{Role: "user", Content: prompt},
	}

	// Create SSE state and writer (for use by RunAgent)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Subscribe to forward token events to the output writer in real-time
	_, ch, ok := sseState.Subscribe()
	streamDone := make(chan struct{})
	if ok {
		go func() {
			defer close(streamDone)
			for evt := range ch {
				if evt.Type == "token" {
					_, _ = fmt.Fprint(out, evt.Content)
				}
			}
		}()
	} else {
		close(streamDone)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	historyMgr := newRequestHistoryManager(req)

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	runErr := RunAgent(runCtx, llm, req, maxTurns, cfg.MaxHistory, w, toolReg, historyMgr, nil, nil, "", cfg.ContextWindowTokens)

	// If streams are still open (e.g., RunAgent returned early due to context
	// cancellation before it could broadcast a done/error event), close them
	// now so the subscriber goroutine terminates.
	if !sseState.Closed() {
		w.Done("batch_complete", runstate.EstimateUsage(sseState.BufferString()))
	}

	// Wait for subscriber goroutine to finish streaming remaining tokens
	<-streamDone

	content := sseState.BufferString()

	if runErr != nil {
		slog.Warn("batch run finished with error",
			slog.String("error", runErr.Error()),
			slog.Int("content_length", len(content)),
		)
	} else {
		slog.Info("batch run completed successfully",
			slog.Int("content_length", len(content)),
		)
	}

	return content, runErr
}
