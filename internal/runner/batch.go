package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
	"github.com/glemsom/eitri/internal/uixt"
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

	// Auto-generate the session ID for this batch run through the unified
	// run-job ID helper (ADR-0025, issue #1108). The ID is path-safety validated
	// and names the persisted session directory (~/.eitri/sessions/<id>/) and the
	// in-memory history session. EITRI_BATCH_SESSION_ID is no longer honoured.
	batchID := s.newRunID(runJobRoleBatch)
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

	// Shared run-preparation seam: identical tool registry, LLM request, and
	// system prompt contract as UI parent runs (issue #1091). No UI session,
	// so render_quick_replies is not registered; the skills service is wired
	// through the RunService so personas with required skills get the skills
	// catalog, the <required_skills> directive, and the skill() tool exactly
	// like the UI.
	prep, err := s.prepareRun(ctx, cfg, runPrepOptions{
		sessionID:     batchID,
		skillCtx:      sessionSkillContext{},
		uiSessionMgr:  nil,
		allowDelegate: true,
	})
	if err != nil {
		return "", err
	}
	llmSvc := prep.llmSvc
	toolReg := prep.toolReg
	fullSystemPrompt := prep.systemPrompt
	req := prep.req

	// Release browser-tool connections (and any other session-scoped tool
	// state) when the batch run ends — same cleanup seam as UI runs.
	defer toolReg.EndSession(batchID)

	// Use the service's HistorySessionMgr or create a local one if nil
	sessionMgr := s.historySessionMgr
	if sessionMgr == nil {
		sessionMgr = history.NewSessionManager(cfg.MaxHistory)
	}
	sessionMgr.Create(batchID)
	sessionMgr.SetSystemPrompt(batchID, fullSystemPrompt)
	sessionMgr.AppendUser(batchID, prompt)
	defer sessionMgr.Close(batchID)

	// Build the unified run-completer and its conversation source (the shared
	// loop.HistoryManager seam, session-manager-backed for batch/UI runs). It
	// drives per-turn snapshots + auto-compaction, the initial snapshot, the
	// terminal snapshot, and the run timeline (issue #1107).
	historyMgr := loop.NewSessionHistoryManager(sessionMgr, batchID)
	completer := &runCompleter{
		svc:          s,
		historyMgr:   historyMgr,
		id:           batchID,
		title:        title,
		systemPrompt: fullSystemPrompt,
		workspace:    workspace,
		startedAt:    batchStartedAt,
		cfg:          cfg,
	}

	// Initial snapshot so the batch session's session.json exists before the
	// first LLM call completes. SaveTrace skips sessions without a snapshot
	// (treated as permanently deleted), so without this the first turn's HTTP
	// traces would be silently dropped (issue #1039).
	completer.persist(uisession.StatusRunning)

	// Store parent config so sub-agents can look up provider/model settings
	s.subagents.StoreParentCfg(batchID, cfg)
	defer s.subagents.DeleteParentCfg(batchID)

	// Create SSE state and writer (for use by RunAgent)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Subscribe to forward token and thinking_delta events to the output
	// writer in real-time. Reasoning deltas from reasoning models are
	// streamed alongside ordinary text, delimited by [thinking]/[/thinking]
	// markers so the two are distinguishable (issue #1095).
	_, ch, ok := sseState.Subscribe()
	streamDone := make(chan struct{})
	if ok {
		go func() {
			defer close(streamDone)
			streamer := &batchStreamer{out: out}
			defer streamer.closeThinking()
			for evt := range ch {
				switch evt.Type {
				case "token":
					streamer.writeToken(evt.Content)
				case "thinking_delta":
					streamer.writeThinking(evt.Content)
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

	runErr := loop.RunAgent(runCtx, loop.RunSpec{
		Client:     llmSvc,
		Request:    req,
		MaxTurns:   maxTurns,
		MaxHistory: cfg.MaxHistory,
		SSEWriter:  w,
		Tools:      toolReg,
	}, loop.RunOpts{
		HistoryMgr:       historyMgr,
		Confirmer:        nil,
		UISessionMgr:     nil,
		SessionID:        batchID,
		RunID:            runstate.GenerateRunID(batchID, batchStartedAt),
		ContextWindow:    cfg.ContextWindowTokens,
		CrashDumpFunc:    s.crashDumpFunc,
		Turns:            &turns,
		DebugLLMDir:      cfg.DebugLLMDir,
		TurnTimeout:      cfg.TurnTimeout,
		CalibrationStore: s.calibrationStore,
		ModelName:        cfg.ModelName,
		RetryPolicy:      &cfg.RetryPolicy,
		TurnCompleter:    completer,
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
	completer.terminal(sseState, status, batchTermination(runErr, runCtx))

	return content, runErr
}

// batchStreamer writes batch-mode stream output to an io.Writer, keeping
// ordinary text and reasoning/thinking deltas distinguishable. Ordinary text
// tokens are written verbatim; reasoning content is wrapped in
// [thinking]...[/thinking] markers (issue #1095). A reasoning block is opened
// on the first thinking delta and closed again when the stream moves back to
// ordinary text (or ends), so the markers delimit each contiguous reasoning
// span — models without reasoning never produce markers and their output is
// unchanged.
type batchStreamer struct {
	out        io.Writer
	inThinking bool
}

const (
	thinkingOpenMarker  = "[thinking]\n"
	thinkingCloseMarker = "[/thinking]\n"
)

// writeToken streams an ordinary text delta, closing an open reasoning block
// first so the reasoning markers delimit exactly the thinking span.
func (b *batchStreamer) writeToken(content string) {
	b.closeThinking()
	_, _ = fmt.Fprint(b.out, content)
}

// writeThinking streams a reasoning delta, opening the reasoning block on the
// first delta of a span.
func (b *batchStreamer) writeThinking(content string) {
	if !b.inThinking {
		_, _ = fmt.Fprint(b.out, thinkingOpenMarker)
		b.inThinking = true
	}
	_, _ = fmt.Fprint(b.out, content)
}

// closeThinking closes an open reasoning block. Idempotent.
func (b *batchStreamer) closeThinking() {
	if !b.inThinking {
		return
	}
	_, _ = fmt.Fprint(b.out, thinkingCloseMarker)
	b.inThinking = false
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
				Message: uixt.MaxTurnsMessage(maxTurnsErr.Limit),
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
