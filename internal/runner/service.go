// Package runner provides RunService - the seam for run lifecycle management.
// It owns agent loop execution, SSE broadcast, session persistence,
// and auth persistence callbacks.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/adapters"
	"github.com/glemsom/eitri/internal/runner/broadcast"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// RunState holds SSE broadcast state and cancel for one run.
type RunState struct {
	SessionID string
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	SSE   *runstate.State
	Turns int // turns consumed so far, updated by agent loop
}

func (rs *RunState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

// PersistAuthFunc is the callback type for persisting refreshed auth state.
type PersistAuthFunc = provider.PersistAuthFunc

type RunServiceDeps struct {
	UISessionMgr      *uisession.Manager
	HistorySessionMgr *history.SessionManager
	SkillsService     *skills.Service
	DebugRecorder     *debug.Recorder               // optional HTTP trace recorder
	CrashDumpFunc     func(err error, stack []byte) // optional; called on fatal agent error
	Persister         *persist.Persister            // optional; writes session snapshots & traces to disk
}

// RunService owns the run lifecycle: agent loop execution,
// SSE broadcast, session persistence, and auth refresh callbacks.
type RunService struct {
	tracker   *runTracker
	broadcast *broadcast.BrowserBroadcaster
	subagents *subagentStore

	confirmMu     sync.Mutex
	confirmations map[string]chan adapters.ConfirmationResult // sessionID → confirmation channel

	uiSessionMgr      *uisession.Manager
	skillsSvc         *skills.Service
	historySessionMgr *history.SessionManager
	debugRecorder     *debug.Recorder
	persistAuth       PersistAuthFunc
	crashDumpFunc     func(err error, stack []byte)
	persister         *persist.Persister // optional; writes session snapshots & traces to disk
}

const completedRunRetention = 5 * time.Second

// NewRunService creates a RunService with the given dependencies.
func NewRunService(deps RunServiceDeps) *RunService {
	return &RunService{
		tracker:           newRunTracker(),
		broadcast:         broadcast.New(),
		subagents:         newSubagentStore(),
		confirmations:     make(map[string]chan adapters.ConfirmationResult),
		uiSessionMgr:      deps.UISessionMgr,
		skillsSvc:         deps.SkillsService,
		historySessionMgr: deps.HistorySessionMgr,
		debugRecorder:     deps.DebugRecorder,
		crashDumpFunc:     deps.CrashDumpFunc,
		persister:         deps.Persister,
	}
}

// SetSkillsService sets the skills service.
func (s *RunService) SetSkillsService(svc *skills.Service) {
	s.skillsSvc = svc
}

// SetUISessionManager sets the UI session manager.
func (s *RunService) SetUISessionManager(mgr *uisession.Manager) {
	s.uiSessionMgr = mgr
}

// SetPersistAuth sets the auth persistence callback.
func (s *RunService) SetPersistAuth(fn PersistAuthFunc) {
	s.persistAuth = fn
}

// SetCrashDumpFunc sets the crash dump callback.
func (s *RunService) SetCrashDumpFunc(fn func(err error, stack []byte)) {
	s.crashDumpFunc = fn
}

// HistorySessionManager returns the history session manager used by the run service.
func (s *RunService) HistorySessionManager() *history.SessionManager {
	return s.historySessionMgr
}

// SubscribeBrowser registers a browser-level SSE subscriber for the given browserID.
// Returns subscriber ID and receive-only channel.
func (s *RunService) SubscribeBrowser(browserID string) (uint64, <-chan broadcast.BrowserEvent) {
	return s.broadcast.Subscribe(browserID)
}

// UnsubscribeBrowser removes a browser-level SSE subscriber.
func (s *RunService) UnsubscribeBrowser(browserID string, id uint64) {
	s.broadcast.Unsubscribe(browserID, id)
}

// BroadcastToBrowser sends an event to all browser-level SSE subscribers for the given browserID.
func (s *RunService) BroadcastToBrowser(browserID string, evt broadcast.BrowserEvent) {
	s.broadcast.Broadcast(browserID, evt)
}

// BrowserSubscribersCount returns the number of browser-level subscribers for logging/diagnostics.
func (s *RunService) BrowserSubscribersCount(browserID string) int {
	return s.broadcast.Count(browserID)
}

// ActiveRun returns the active RunState for a session, or nil if none.
func (s *RunService) ActiveRun(sessionID string) *RunState {
	return s.tracker.getActive(sessionID)
}

// lookupRun returns the run state without checking if done.
func (s *RunService) lookupRun(sessionID string) *RunState {
	return s.tracker.get(sessionID)
}

// Subscribe attaches an SSE subscriber for an active run.
// Returns (subscriberID, channel, ok).
func (s *RunService) Subscribe(sessionID string) (uint64, <-chan runstate.SSEEvent, bool) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return 0, nil, false
	}
	return state.SSE.Subscribe()
}

// Unsubscribe removes an SSE subscriber.
func (s *RunService) Unsubscribe(sessionID string, id uint64) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.Unsubscribe(id)
}

// AppendEvent is no longer used. The agent loop owns SSE broadcast directly.
// Deprecated: kept for backward compatibility in tests; always returns "".
func (s *RunService) AppendEvent(state *RunState) string {
	return ""
}

func (s *RunService) Cancel(sessionID string) bool {
	state := s.tracker.removeRun(sessionID)
	if state == nil {
		return false
	}

	// Close any pending confirmation channel
	s.confirmMu.Lock()
	if ch, ok := s.confirmations[sessionID]; ok {
		close(ch)
		delete(s.confirmations, sessionID)
	}
	s.confirmMu.Unlock()

	// Cancel any sub-agents spawned by this session
	s.subagents.CancelForSession(sessionID)

	slog.Info("run canceled", slog.String("session_id", sessionID))
	state.SSE.BroadcastDone("", nil)
	state.Cancel()
	state.finish()
	return true
}

func (s *RunService) CancelAll() {
	states := s.tracker.removeAll()
	for _, state := range states {
		slog.Info("run canceled", slog.String("session_id", state.SessionID))
		state.Cancel()
		state.finish()
	}
}

func (s *RunService) ActiveRunCount() int {
	return s.tracker.count()
}

func (s *RunService) LastBatchConversationContext() *debug.ConversationContext {
	ctx := s.tracker.getBatchCtx()
	if ctx == nil {
		return nil
	}
	return &debug.ConversationContext{
		LastUserMessage:      ctx.LastUserMessage,
		LastAssistantMessage: ctx.LastAssistantMessage,
		TurnNumber:           ctx.TurnNumber,
	}
}

func (s *RunService) setBatchConversationContext(ctx *debug.ConversationContext) {
	s.tracker.setBatchCtx(&debugConversationContext{
		LastUserMessage:      ctx.LastUserMessage,
		LastAssistantMessage: ctx.LastAssistantMessage,
		TurnNumber:           ctx.TurnNumber,
	})
}

func (s *RunService) ActiveRunSSECounters() map[string]struct{ SubscriberCount, ReplayCount uint64 } {
	return s.tracker.sseCounters()
}

// CompletedRunRetentionMs returns the completed run retention duration in milliseconds.
func (s *RunService) CompletedRunRetentionMs() int64 {
	return completedRunRetention.Milliseconds()
}

// RunSSESnapshot holds SSE diagnostic counters and history for one active run,
// collected atomically under RunService.mu.
type RunSSESnapshot struct {
	SubscriberCount uint64
	ReplayCount     uint64
	History         []runstate.SSEEvent
	Busy            bool
	Turns           int
	PendingApproval bool
}

func (s *RunService) ActiveRunSSESnapshot(sessionID string) *RunSSESnapshot {
	snap := s.tracker.sseSnapshot(sessionID)
	if snap == nil {
		return nil
	}
	snap.PendingApproval = s.HasPendingConfirmation(sessionID)
	return snap
}

// HasPendingConfirmation returns true if there is a pending confirmation
// for the given session.
func (s *RunService) HasPendingConfirmation(sessionID string) bool {
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	_, ok := s.confirmations[sessionID]
	return ok
}

// CloseSession cancels the active run and closes the session.
func (s *RunService) CloseSession(sessionID string) error {
	s.Cancel(sessionID)
	if s.historySessionMgr != nil {
		s.historySessionMgr.Close(sessionID)
	}
	return nil
}

// NotifySessionClosed broadcasts a closed event for a session.
func (s *RunService) NotifySessionClosed(sessionID, message string) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.BroadcastClosed(message)
}

func (s *RunService) NotifyAllStreamsClosed(message string) {
	s.tracker.notifyAllClosed(message)
}

// confirmPath implements ConfirmationFunc for RunAgent.
// It creates a channel for the session, sends a needs_confirmation SSE event,
// and blocks waiting for the user's response via the API endpoint.
func (s *RunService) confirmPath(ctx context.Context, sessionID, path, message string) (*adapters.ConfirmationResult, error) {
	s.confirmMu.Lock()
	// Check if channel already exists (should not happen in normal flow)
	if existing, ok := s.confirmations[sessionID]; ok {
		close(existing)
	}
	ch := make(chan adapters.ConfirmationResult, 1)
	s.confirmations[sessionID] = ch
	s.confirmMu.Unlock()

	// Clean up when done
	defer func() {
		s.confirmMu.Lock()
		delete(s.confirmations, sessionID)
		s.confirmMu.Unlock()
	}()

	select {
	case result := <-ch:
		return &result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ResolveConfirmation resolves a pending confirmation for a session.
// Called by the API endpoint when the user allows or denies a path.
func (s *RunService) ResolveConfirmation(sessionID, path string, approved bool) bool {
	s.confirmMu.Lock()
	ch, ok := s.confirmations[sessionID]
	s.confirmMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- adapters.ConfirmationResult{Path: path, Approved: approved}:
		return true
	default:
		return false
	}
}

// CompactSession compacts tool-result messages in a session's conversation history
// manually. Unlike auto-compaction (which runs after each agent turn), this method
// is invoked on demand via the /compact slash command or the "Compact now" button.
//
// It builds a minimal LLM service (no tools, no system prompt construction) for
// summarization, runs the compactor, replaces the history, snapshots the result,
// and returns the number of messages compacted and the approximate number of tokens freed.
func (s *RunService) CompactSession(ctx context.Context, sessionID string, cfg runconfig.RunConfig) (compactedCount int, freedTokens int, _ error) {
	if s.historySessionMgr == nil {
		return 0, 0, fmt.Errorf("history session manager not available")
	}

	// Build a minimal LLM service for summarization (no tools needed).
	llmSvc, err := newCompactLLMService(ctx, cfg, s.persistAuth)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create LLM service for compaction: %w", err)
	}

	historyMsgs := s.historySessionMgr.History(sessionID)
	if historyMsgs == nil {
		return 0, 0, fmt.Errorf("session %q not found in history manager", sessionID)
	}

	contextWindow := cfg.ContextWindowTokens
	if contextWindow <= 0 {
		contextWindow = 256000
	}
	highWater := contextWindow * cfg.CompactionThresholdPercent / 100
	lowWater := contextWindow * cfg.CompactionLowWaterPercent / 100

	if highWater <= 0 {
		highWater = contextWindow * 90 / 100
	}
	if lowWater <= 0 {
		lowWater = contextWindow * 30 / 100
	}

	compactedMsgs, count, freed, compErr := compactor.New().Compact(ctx, historyMsgs, llmSvc, compactor.Thresholds{
		HighWater: highWater,
		LowWater:  lowWater,
	})
	if compErr != nil {
		return 0, 0, fmt.Errorf("compaction failed: %w", compErr)
	}
	if compactedMsgs == nil || count == 0 {
		return 0, 0, nil
	}

	// Replace in-memory history with compacted version.
	s.historySessionMgr.RestoreHistory(sessionID, compactedMsgs)

	// Snapshot the compacted history if persister is available.
	if s.persister != nil {
		sessAfter := s.uiSessionMgr.Get(sessionID)
		historyAfter := s.historySessionMgr.History(sessionID)
		if sessAfter != nil && historyAfter != nil {
			if err := s.persister.SnapshotSession(sessionID, sessAfter, historyAfter); err != nil {
				slog.Warn("failed to snapshot compacted session",
					slog.String("session_id", sessionID),
					slog.Any("error", err),
				)
			}
		}
	}

	return count, freed, nil
}

// newCompactLLMService creates a bare LLM service for summarization without
// tool registries, system prompts, or skill context.
func newCompactLLMService(ctx context.Context, cfg runconfig.RunConfig, persistAuth PersistAuthFunc) (llm.LLMService, error) {
	reqAuth := provider.ResolveAuthRequest{
		ProviderID:   cfg.ProviderID,
		APIKey:       cfg.APIKey,
		ProviderAuth: cfg.ProviderAuth,
	}
	resolvedKey, _, err := provider.ResolveAuth(ctx, reqAuth, persistAuth)
	if err != nil {
		return nil, fmt.Errorf("auth resolution: %w", err)
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

	return llm.NewLLMService(adapterCfg)
}
