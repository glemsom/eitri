package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"

	"github.com/glemsom/eitri/internal/tool"
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

	// Resolve the session ID for this batch run: default batch-<unixnano>,
	// overridable via EITRI_BATCH_SESSION_ID (validated so it cannot escape
	// the sessions directory). The ID names the persisted session directory
	// (~/.eitri/sessions/<id>/) and the in-memory history session.
	batchID, err := batchSessionID()
	if err != nil {
		return "", err
	}
	batchStartedAt := time.Now()
	rootDir := "~/.eitri"
	if s.persister != nil {
		rootDir = s.persister.RootDir()
	}
	slog.Info("batch session",
		slog.String("session_id", batchID),
		slog.String("session_dir", filepath.Join(rootDir, "sessions", batchID)),
	)

	// Title derives from the prompt using the same rule as UI session titles
	// (session.TitlePreview, exported for headless runs — issue #1038).
	title := batchTitle(prompt, batchID)
	workspace := cfg.Workspace

	// Build LLM service, tool registry, and system prompt (no skill activations in batch mode).
	// The session ID and recorder are passed so headless batch runs feed the same
	// trace recorder and interaction metrics as browser runs (issue #987).
	llmSvc, toolReg, fullSystemPrompt, err := buildLLMService(ctx, cfg, batchID, s.debugRecorder, s.persistAuth, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr, sessionSkillContext{})
	if err != nil {
		return "", err
	}

	// Register delegate/collect tools for batch mode (parent agent needs sub-agent support)
	toolReg.Register(tool.NewDelegate(s))
	toolReg.Register(tool.NewCollect(s))

	// Create request (streaming only — history is managed by sessionHistoryManager)
	req := &litellm.Request{
		Model: cfg.ModelName,
	}
	if cfg.ThinkingLevel != "" {
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

	// Use the service's HistorySessionMgr or create a local one if nil
	sessionMgr := s.historySessionMgr
	if sessionMgr == nil {
		sessionMgr = history.NewSessionManager(cfg.MaxHistory)
	}
	sessionMgr.Create(batchID)
	sessionMgr.SetSystemPrompt(batchID, fullSystemPrompt)
	sessionMgr.AppendUser(batchID, prompt)
	defer sessionMgr.Close(batchID)

	// Initial snapshot so the batch session's session.json exists before the
	// first LLM call completes. SaveTrace skips sessions without a snapshot
	// (treated as permanently deleted), so without this the first turn's HTTP
	// traces would be silently dropped (issue #1039).
	s.batchSnapshot(sessionMgr, batchID, uisession.StatusRunning, title, workspace, fullSystemPrompt, batchStartedAt)

	// Store parent config so sub-agents can look up provider/model settings
	s.subagents.StoreParentCfg(batchID, cfg)
	defer s.subagents.DeleteParentCfg(batchID)

	// Wrap in a sessionHistoryManager (same adapter the UI path uses)
	historyAdapter := loop.NewSessionHistoryManager(sessionMgr, batchID)

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
	// Pass the batch ID as the session ID in the run context so tools
	// (e.g. delegate/collect for sub-agents) can resolve the parent run
	// config that was registered under batchID above (issue #1001).
	runCtx = context.WithValue(runCtx, tool.SessionIDKey, batchID)
	defer cancel()

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	// Track turns for conversation context
	var turns int
	runID := runstate.GenerateRunID(batchID, batchStartedAt)

	// Per-turn snapshot seam: after each complete agent turn the batch run's
	// conversation is persisted to disk as session.json (issue #1039), via
	// the same completion seam the UI path uses — no agent-loop changes.
	turnCompleter := &batchTurnCompleter{
		svc:          s,
		sessionMgr:   sessionMgr,
		sessionID:    batchID,
		title:        title,
		workspace:    workspace,
		systemPrompt: fullSystemPrompt,
		createdAt:    batchStartedAt,
	}

	runErr := loop.RunAgent(runCtx, loop.RunSpec{
		Client:     llmSvc,
		Request:    req,
		MaxTurns:   maxTurns,
		MaxHistory: cfg.MaxHistory,
		SSEWriter:  w,
		Tools:      toolReg,
	}, loop.RunOpts{
		HistoryMgr:       historyAdapter,
		Confirmer:        nil,
		UISessionMgr:     nil,
		SessionID:        batchID,
		RunID:            runID,
		ContextWindow:    cfg.ContextWindowTokens,
		CrashDumpFunc:    nil,
		Turns:            &turns,
		DebugLLMDir:      cfg.DebugLLMDir,
		TurnTimeout:      cfg.TurnTimeout,
		CalibrationStore: s.calibrationStore,
		ModelName:        cfg.ModelName,
		RetryPolicy:      &cfg.RetryPolicy,
		TurnCompleter:    turnCompleter,
	})

	// If streams are still open (e.g., RunAgent returned early due to context
	// cancellation before it could broadcast a done/error event), close them
	// now so the subscriber goroutine terminates.
	if !sseState.Closed() {
		w.Done("batch_complete", runstate.EstimateUsage(sseState.BufferString(), s.calibrationStore, cfg.ModelName))
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

	// Terminal snapshot + timeline on every exit path (success and failure).
	// The snapshot reflects the final status: idle on success, error on any
	// failure. The timeline carries the specific termination reason
	// (completed / cancelled / max-turns / error) matching UI behaviour.
	status := uisession.StatusIdle
	if runErr != nil {
		status = uisession.StatusError
	}
	s.batchSnapshot(sessionMgr, batchID, status, title, workspace, fullSystemPrompt, batchStartedAt)
	s.persistRunTimeline(batchID, runID, batchStartedAt, sseState, cfg, batchTermination(runErr, runCtx))

	return content, runErr
}

// batchTermination classifies a batch run's outcome into the timeline
// termination reason, matching the UI exit paths (issue #1039).
func batchTermination(runErr error, runCtx context.Context) *runstate.TimelineTermination {
	switch {
	case runErr == nil:
		return &runstate.TimelineTermination{Reason: runstate.TerminationCompleted}

	case runCtx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded):
		return &runstate.TimelineTermination{
			Reason:  runstate.TerminationCancelled,
			Message: "Run cancelled by user or context deadline exceeded",
		}

	default:
		var maxTurnsErr *loop.MaxTurnsExceededError
		if errors.As(runErr, &maxTurnsErr) {
			return &runstate.TimelineTermination{
				Reason:  runstate.TerminationMaxTurns,
				Message: runstate.MaxTurnsMessage(maxTurnsErr.Limit),
			}
		}
		return &runstate.TimelineTermination{
			Reason:  runstate.TerminationError,
			Message: runErr.Error(),
		}
	}
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
				lastUser = msg.Content()
			}
		case "assistant":
			if lastAssistant == "" {
				lastAssistant = msg.Content()
			}
		}
		if lastUser != "" && lastAssistant != "" {
			break
		}
	}
	return lastUser, lastAssistant
}
