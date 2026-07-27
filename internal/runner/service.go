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

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tokenizer"
)

// RunState holds SSE broadcast state and cancel for one run.
type RunState struct {
	SessionID string
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	// RunCfg holds the configuration for this run, used by the TurnCompleter
	// for auto-compaction and other post-turn tasks.
	RunCfg RunConfig

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
	CalibrationStore  *tokenizer.CalibrationStore   // optional; per-model CPT calibration
}

// RunService owns the run lifecycle: agent loop execution,
// SSE broadcast, session persistence, and auth refresh callbacks.
type RunService struct {
	// Active runs map with concurrency-safe access (formerly runTracker).
	mu     sync.Mutex
	active map[string]*RunState

	// Batch conversation context tracking (set by BatchRun, consumed by crash dump).
	batchCtxMu   sync.Mutex
	batchLastCtx *debug.ConversationContext

	broadcast *BrowserBroadcaster
	subagents *subagentStore

	confirmMu     sync.Mutex
	confirmations map[string]chan loop.ConfirmationResult // sessionID → confirmation channel

	uiSessionMgr      *uisession.Manager
	skillsSvc         *skills.Service
	historySessionMgr *history.SessionManager
	debugRecorder     *debug.Recorder
	persistAuth       PersistAuthFunc
	crashDumpFunc     func(err error, stack []byte)
	persister         *persist.Persister // optional; writes session snapshots & traces to disk
	calibrationStore  *tokenizer.CalibrationStore // optional; per-model CPT calibration
}

const completedRunRetention = 5 * time.Second

// NewRunService creates a RunService with the given dependencies.
func NewRunService(deps RunServiceDeps) *RunService {
	return &RunService{
		active:            make(map[string]*RunState),
		broadcast:         New(),
		subagents:         newSubagentStore(),
		confirmations:     make(map[string]chan loop.ConfirmationResult),
		uiSessionMgr:      deps.UISessionMgr,
		skillsSvc:         deps.SkillsService,
		historySessionMgr: deps.HistorySessionMgr,
		debugRecorder:     deps.DebugRecorder,
		crashDumpFunc:     deps.CrashDumpFunc,
		persister:         deps.Persister,
		calibrationStore:  deps.CalibrationStore,
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
func (s *RunService) SubscribeBrowser(browserID string) (uint64, <-chan BrowserEvent) {
	return s.broadcast.Subscribe(browserID)
}

// UnsubscribeBrowser removes a browser-level SSE subscriber.
func (s *RunService) UnsubscribeBrowser(browserID string, id uint64) {
	s.broadcast.Unsubscribe(browserID, id)
}

// BroadcastToBrowser sends an event to all browser-level SSE subscribers for the given browserID.
func (s *RunService) BroadcastToBrowser(browserID string, evt BrowserEvent) {
	s.broadcast.Broadcast(browserID, evt)
}

// BrowserSubscribersCount returns the number of browser-level subscribers for logging/diagnostics.
func (s *RunService) BrowserSubscribersCount(browserID string) int {
	return s.broadcast.Count(browserID)
}

// ActiveRun returns the active RunState for a session, or nil if none.
func (s *RunService) ActiveRun(sessionID string) *RunState {
	return s.getActive(sessionID)
}

// lookupRun returns the run state without checking if done.
func (s *RunService) lookupRun(sessionID string) *RunState {
	return s.get(sessionID)
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
	state := s.removeRun(sessionID)
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
	states := s.removeAll()
	for _, state := range states {
		slog.Info("run canceled", slog.String("session_id", state.SessionID))
		state.Cancel()
		state.finish()
	}
}

func (s *RunService) ActiveRunCount() int {
	return s.count()
}

func (s *RunService) LastBatchConversationContext() *debug.ConversationContext {
	ctx := s.getBatchCtx()
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
	s.setBatchCtx(ctx)
}

func (s *RunService) ActiveRunSSECounters() map[string]struct{ SubscriberCount, ReplayCount uint64 } {
	return s.sseCounters()
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
	snap := s.sseSnapshot(sessionID)
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

// LoadSessionFromDisk reads a session snapshot from disk via the persister,
// adds it to the UI session manager (with status forced to idle), and restores
// its conversation history in the history manager.
// Returns the loaded session, or an error if the session doesn't exist on disk
// or if reading/parsing fails.
func (s *RunService) LoadSessionFromDisk(sessionID string) (*uisession.UISession, error) {
	if s.persister == nil {
		return nil, fmt.Errorf("persister not available")
	}

	data, err := s.persister.LoadSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("cannot load session from disk: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("session %s not found on disk", sessionID)
	}

	loaded, err := s.uiSessionMgr.LoadFromDisk(data)
	if err != nil {
		return nil, fmt.Errorf("cannot restore session to manager: %w", err)
	}

	// Restore conversation history in the history manager
	if s.historySessionMgr != nil {
		convo := s.uiSessionMgr.GetConversation(sessionID)
		if convo != nil {
			msgs := make([]message.Message, 0, len(convo.Messages))
			for _, m := range convo.Messages {
				msgs = append(msgs, message.Message{
					Role:       m.Role,
					Content:    m.Content,
					ToolCallID: m.ToolCallID,
					ToolCalls:  m.ToolCalls,
				})
			}
			s.historySessionMgr.RestoreHistory(sessionID, msgs)
		}
	}

	return loaded, nil
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
	s.notifyAllClosed(message)
}

// ── Inline run-tracker methods ──────────────────────────────────────────────
// These were previously on a separate runTracker type. They now live directly
// on RunService for simplicity. No behavior change.

// get returns the RunState for a session without checking if done.
func (s *RunService) get(sessionID string) *RunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessionID]
}

// getActive returns the RunState for a session if it exists and is not done.
func (s *RunService) getActive(sessionID string) *RunState {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	s.mu.Unlock()
	if !exists {
		return nil
	}
	select {
	case <-state.Done:
		return nil
	default:
		return state
	}
}

// store inserts a RunState for a session.
func (s *RunService) store(sessionID string, state *RunState) {
	s.mu.Lock()
	s.active[sessionID] = state
	s.mu.Unlock()
}

// remove deletes the RunState for a session if it matches the given pointer.
func (s *RunService) remove(sessionID string, state *RunState) {
	s.mu.Lock()
	if s.active[sessionID] == state {
		delete(s.active, sessionID)
	}
	s.mu.Unlock()
}

// exchangeIfDone deletes the run state if it's done, returning true if removed.
func (s *RunService) exchangeIfDone(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, exists := s.active[sessionID]; exists {
		select {
		case <-existing.Done:
			delete(s.active, sessionID)
			return true
		default:
			return false
		}
	}
	return false
}

// removeRun removes and returns the RunState for a session.
func (s *RunService) removeRun(sessionID string) *RunState {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	if exists {
		delete(s.active, sessionID)
	}
	s.mu.Unlock()
	if !exists {
		return nil
	}
	return state
}

// removeAll returns all RunStates and clears the map.
func (s *RunService) removeAll() []*RunState {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for sessionID, state := range s.active {
		delete(s.active, sessionID)
		states = append(states, state)
	}
	s.mu.Unlock()
	return states
}

// count returns the number of active runs.
func (s *RunService) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

// allActiveStates returns all non-done RunStates.
func (s *RunService) allActiveStates() []*RunState {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for _, state := range s.active {
		select {
		case <-state.Done:
		default:
			states = append(states, state)
		}
	}
	s.mu.Unlock()
	return states
}

// sseCounters returns subscriber and replay counts for all active runs.
func (s *RunService) sseCounters() map[string]struct{ SubscriberCount, ReplayCount uint64 } {
	s.mu.Lock()
	result := make(map[string]struct{ SubscriberCount, ReplayCount uint64 }, len(s.active))
	for sessionID, state := range s.active {
		select {
		case <-state.Done:
		default:
			result[sessionID] = struct{ SubscriberCount, ReplayCount uint64 }{
				SubscriberCount: state.SSE.SubscriberCount(),
				ReplayCount:     state.SSE.ReplayCount(),
			}
		}
	}
	s.mu.Unlock()
	return result
}

// sseSnapshot returns a snapshot of SSE counters, history, and run state for a session.
func (s *RunService) sseSnapshot(sessionID string) *RunSSESnapshot {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	select {
	case <-state.Done:
		s.mu.Unlock()
		return nil
	default:
	}

	history := state.SSE.History()
	if len(history) > 50 {
		history = history[len(history)-50:]
	}

	turns := state.Turns
	s.mu.Unlock()

	return &RunSSESnapshot{
		SubscriberCount: state.SSE.SubscriberCount(),
		ReplayCount:     state.SSE.ReplayCount(),
		History:         history,
		Busy:            true,
		Turns:           turns,
	}
}

// setBatchCtx stores the conversation context for the last batch run.
func (s *RunService) setBatchCtx(ctx *debug.ConversationContext) {
	s.batchCtxMu.Lock()
	defer s.batchCtxMu.Unlock()
	s.batchLastCtx = ctx
}

// getBatchCtx returns the conversation context from the last batch run.
func (s *RunService) getBatchCtx() *debug.ConversationContext {
	s.batchCtxMu.Lock()
	defer s.batchCtxMu.Unlock()
	return s.batchLastCtx
}

// notifyAllClosed broadcasts a closed event to all active sessions.
func (s *RunService) notifyAllClosed(message string) {
	for _, state := range s.allActiveStates() {
		state.SSE.BroadcastClosed(message)
	}
}

// broadcastStatusUpdate broadcasts a session status change to browser-level subscribers.
func (s *RunService) broadcastStatusUpdate(sessionID string, status uisession.Status, uiSessionMgr *uisession.Manager, bb *BrowserBroadcaster) {
	if uiSessionMgr == nil {
		return
	}
	uiSessionMgr.UpdateStatus(sessionID, status)
	meta := uiSessionMgr.GetMeta(sessionID)
	if meta == nil || meta.BrowserID == "" {
		return
	}
	bb.Broadcast(meta.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(meta.Status),
		},
	})
}

// confirmPath implements ConfirmationFunc for RunAgent.
// It creates a channel for the session, sends a needs_confirmation SSE event,
// and blocks waiting for the user's response via the API endpoint.
func (s *RunService) confirmPath(ctx context.Context, sessionID, path, message string) (*loop.ConfirmationResult, error) {
	s.confirmMu.Lock()
	// Check if channel already exists (should not happen in normal flow)
	if existing, ok := s.confirmations[sessionID]; ok {
		close(existing)
	}
	ch := make(chan loop.ConfirmationResult, 1)
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
	case ch <- loop.ConfirmationResult{Path: path, Approved: approved}:
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
// and returns the number of messages compacted, approximate number of tokens freed,
// and the number of tool-call argument blocks pruned.
func (s *RunService) CompactSession(ctx context.Context, sessionID string, cfg RunConfig) (compactedCount int, freedTokens int, prunedToolCalls int, _ error) {
	if s.historySessionMgr == nil {
		return 0, 0, 0, fmt.Errorf("history session manager not available")
	}

	// Build a minimal LLM service for summarization (no tools needed).
	llmSvc, err := newCompactLLMService(ctx, cfg, s.persistAuth)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to create LLM service for compaction: %w", err)
	}

	historyMsgs := s.historySessionMgr.History(sessionID)
	if historyMsgs == nil {
		return 0, 0, 0, fmt.Errorf("session %q not found in history manager", sessionID)
	}

	// Convert to flat messages for the compactor
	flatMsgs := make([]message.Message, len(historyMsgs))
	for i, em := range historyMsgs {
		flatMsgs[i] = em.ToMessage()
	}

	// Manual compaction always runs — no high-water gate.
	// LowWater=0 means the compactor will compact until no more
	// tool results remain (or the default low-water logic activates).
	compactedMsgs, count, freed, prunedCount, compErr := compactor.New().Compact(ctx, flatMsgs, llmSvc, compactor.Thresholds{
		HighWater:                0,
		LowWater:                 0,
		MessageSizeThreshold:     cfg.CompactionMessageSizeThreshold,
		ToolCallRetentionTurns:   cfg.CompactionToolCallRetentionTurns,
		SalienceEnabled:          cfg.CompactionSalienceEnabled,
	})
	if compErr != nil {
		return 0, 0, 0, fmt.Errorf("compaction failed: %w", compErr)
	}
	if compactedMsgs == nil || (count == 0 && prunedCount == 0) {
		return 0, 0, 0, nil
	}

	// Replace in-memory history with compacted version.
	s.historySessionMgr.RestoreHistory(sessionID, compactedMsgs)

	// Snapshot the compacted history if persister is available.
	if s.persister != nil {
		sessAfter := s.uiSessionMgr.Get(sessionID)
		if sessAfter != nil {
			if err := s.persister.SnapshotSession(sessionID, sessAfter); err != nil {
				slog.Warn("failed to snapshot compacted session",
					slog.String("session_id", sessionID),
					slog.Any("error", err),
				)
			}
		}
	}

	return count, freed, prunedCount, nil
}

// newCompactLLMService creates a bare LLM service for summarization without
// tool registries, system prompts, or skill context.
func newCompactLLMService(ctx context.Context, cfg RunConfig, persistAuth PersistAuthFunc) (*litellm.Client, error) {
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

	litellmCfg := provider.LitellmConfig{
		ProviderID:   cfg.ProviderID,
		Model:        cfg.ModelName,
		BaseURL:      cfg.BaseURL,
		APIKey:       apiKey,
		DebugPrompt:  cfg.DebugPrompt,
		DebugRequest: cfg.DebugRequest,
		DebugLLMDir:  cfg.DebugLLMDir,
	}

	client, err := provider.NewLitellmClient(litellmCfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
