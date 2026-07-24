package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runstate"
)

// BatchRun runs a single prompt in headless batch mode.
// It uses sessionHistoryManager (wrapping history.SessionManager) to store
// conversation history, streams text tokens to the supplied io.Writer as they
// arrive, and blocks until the agent loop finishes or context is cancelled.
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
		return "", errors.New("provider not configured: set base_url and model in settings")
	}

	// Build LLM service, tool registry, and system prompt (no skill activations in batch mode)
	llmSvc, toolReg, fullSystemPrompt, err := buildLLMService(ctx, cfg, "", nil, s.persistAuth, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr, sessionSkillContext{})
	if err != nil {
		return "", err
	}

	// Create request (streaming only — history is managed by sessionHistoryManager)
	req := &llm.Request{
		Model:  cfg.ModelName,
		Stream: true,
	}
	if cfg.ThinkingLevel != "" {
		req.ReasoningEffort = cfg.ThinkingLevel
	}

	// Generate a unique session ID for this batch run
	batchID := fmt.Sprintf("batch-%d", time.Now().UnixNano())

	// Use the service's HistorySessionMgr or create a local one if nil
	sessionMgr := s.historySessionMgr
	if sessionMgr == nil {
		sessionMgr = history.NewSessionManager(cfg.MaxHistory)
	}
	sessionMgr.Create(batchID)
	sessionMgr.SetSystemPrompt(batchID, fullSystemPrompt)
	sessionMgr.AppendUser(batchID, prompt)
	defer sessionMgr.Close(batchID)

	// Wrap in a sessionHistoryManager (same adapter the UI path uses)
	historyAdapter := newSessionHistoryManager(sessionMgr, nil, batchID)

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

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	// Track turns for conversation context
	var turns int
	runErr := RunAgent(runCtx, RunSpec{
		Service:    llmSvc,
		Request:    req,
		MaxTurns:   maxTurns,
		MaxHistory: cfg.MaxHistory,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    historyAdapter,
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     batchID,
		ContextWindow: cfg.ContextWindowTokens,
		CrashDumpFunc: nil,
		Turns:         &turns,
	})

	// If streams are still open (e.g., RunAgent returned early due to context
	// cancellation before it could broadcast a done/error event), close them
	// now so the subscriber goroutine terminates.
	if !sseState.Closed() {
		w.Done("batch_complete", runstate.EstimateUsage(sseState.BufferString()))
	}

	// Wait for subscriber goroutine to finish streaming remaining tokens
	<-streamDone

	content := sseState.BufferString()

	// Capture conversation context before session is closed
	lastUserMsg, lastAssistantMsg := extractLastMessages(sessionMgr, batchID)
	s.setBatchConversationContext(&debug.ConversationContext{
		LastUserMessage:      lastUserMsg,
		LastAssistantMessage: lastAssistantMsg,
		TurnNumber:           turns,
	})

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

// extractLastMessages extracts the last user and assistant messages from the
// session manager's history for the given session ID. Returns empty strings
// if no messages of that role are found.
func extractLastMessages(sessionMgr *history.SessionManager, sessionID string) (lastUser, lastAssistant string) {
	history := sessionMgr.History(sessionID)
	if history == nil {
		return "", ""
	}
	// Walk backwards through history to find the last user and assistant messages.
	// The system prompt is the first message; skip it.
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		switch msg.Role {
		case "user":
			if lastUser == "" {
				lastUser = msg.Content
			}
		case "assistant":
			if lastAssistant == "" {
				lastAssistant = msg.Content
			}
		}
		if lastUser != "" && lastAssistant != "" {
			break
		}
	}
	return lastUser, lastAssistant
}
