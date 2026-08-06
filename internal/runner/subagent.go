package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/timeline"
	"github.com/glemsom/eitri/internal/tool"
	"github.com/glemsom/eitri/internal/uixt"
)

// subAgentStatus tracks the lifecycle of a sub-agent task.
type subAgentStatus string

const (
	subAgentRunning   subAgentStatus = "running"
	subAgentCompleted subAgentStatus = "completed"
	subAgentError     subAgentStatus = "error"
	subAgentCancelled subAgentStatus = "cancelled"
)

// subAgentRecord tracks one in-flight sub-agent spawned via delegate().
type subAgentRecord struct {
	TaskID         string
	SessionID      string
	ChildSessionID string // UI session ID if child session created
	Status         subAgentStatus
	Result         string
	TurnCount      int
	Err            error
	Done           chan struct{}
	Cancel         context.CancelFunc
	StartedAt      time.Time
}

func (r *subAgentRecord) finish() {
	// Non-blocking close; idempotent via select.
	select {
	case <-r.Done:
	default:
		close(r.Done)
	}
}

// SubAgentResult is the result type for CollectSubAgents, aliased from the tool package.
type SubAgentResult = tool.SubAgentResult

// subAgentReapTTL controls how long completed sub-agent records are kept
// after finishing before they are automatically reaped.
const subAgentReapTTL = 30 * time.Second

// Since issue #1107, sub-agent persistence is handled by the unified
// run-completer (internal/runner/run_completer.go) wired with a request-based
// history manager; the former subAgentSnapshotter no longer exists.

// SpawnSubAgent starts a sub-agent in the background to complete the given task.
// Returns a unique task ID immediately. The sub-agent runs with its own LLM
// service, tool registry (base + skill — no delegate/collect/quick_replies,
// so sub-agents cannot recurse or emit UI-only tools), and request-based
// history manager (no browser session persistence).
//
// personaName is an optional persona name. If non-empty, the sub-agent resolves
// that persona from disk and uses its system prompt + injected skills. If the
// persona file is missing or corrupt, a warning is logged and the sub-agent
// falls back to generic. If empty, the sub-agent uses the parent's active
// persona (or generic).
//
// Cancelling the parent run cascades to cancel all in-flight sub-agents.
func (s *RunService) SpawnSubAgent(ctx context.Context, sessionID, task string, maxTurns int, personaName string) (taskID string, err error) {
	// Retrieve parent config for this session
	parentCfg, ok := s.subagents.GetParentCfg(sessionID)
	if !ok {
		return "", fmt.Errorf("no parent run config found for session %s", sessionID)
	}

	// Resolve persona if specified
	if personaName != "" {
		resolved, err := persona.LoadWithHome(parentCfg.Workspace, resolveHomeDir(parentCfg.HomeDir), personaName)
		if err != nil {
			slog.Warn("sub-agent persona not found, falling back to generic",
				slog.String("persona", personaName),
				slog.Any("error", err),
			)
			parentCfg.ActivePersona = persona.GenericName
		} else {
			slog.Info("sub-agent using persona",
				slog.String("persona", personaName),
				slog.Int("required_skills", len(resolved.RequiredSkills)),
			)
			parentCfg.ActivePersona = personaName
		}
	}

	// Generate the sub-agent task ID through the unified run-job ID helper
	// (ADR-0025, issue #1108): auto-generated, unique, and path-safe.
	taskID = s.newRunID(runJobRoleSubagent)

	slog.Info("spawning sub-agent",
		slog.String("task_id", taskID),
		slog.String("parent_session", sessionID),
		slog.String("task", loop.TruncateText(task, 100)),
		slog.Int("max_turns", maxTurns),
	)

	// Prepare the delegated run through the same run-preparation seam as UI and
	// batch parent runs, but as a leaf (allowDelegate false): the toolset is
	// the base registry + skill — no delegate, no collect, and never
	// render_quick_replies. The task ID and recorder are passed so sub-agent
	// LLM calls feed the same trace recorder and interaction metrics as their
	// parent (issue #987).
	prep, err := s.prepareRun(ctx, parentCfg, runPrepOptions{
		sessionID:     taskID,
		skillCtx:      sessionSkillContext{},
		uiSessionMgr:  s.uiSessionMgr,
		allowDelegate: false,
	})
	if err != nil {
		return "", fmt.Errorf("sub-agent run prep: %w", err)
	}
	llmSvc := prep.llmSvc
	toolReg := prep.toolReg

	// Append task-specific suffix to the base system prompt.
	systemPrompt := prep.systemPrompt + "\n\nYou are performing the following task: " + task

	// Create request and set up messages. The request (model, max_output_tokens,
	// prompt-cache key, thinking level) is assembled by the same shared builder
	// the UI and batch parent runs use (issue #1091).
	req := prep.req
	req.Messages = []litellm.Message{
		{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: systemPrompt}}},
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: task}}},
	}

	sseState := runstate.New()
	subCtx, cancel := context.WithCancel(ctx)
	// Key the sub-agent's tools (bash /tmp namespace, browser allocator) by its
	// own task ID so its /tmp namespace is isolated from the parent's and matches
	// the EndSession(taskID) cleanup below. The parent's session ID would
	// otherwise leak through the inherited context (ADR-0026).
	subCtx = context.WithValue(subCtx, tool.SessionIDKey, taskID)

	record := &subAgentRecord{
		TaskID:    taskID,
		SessionID: sessionID,
		Status:    subAgentRunning,
		Done:      make(chan struct{}),
		Cancel:    cancel,
		StartedAt: time.Now(),
	}

	s.subagents.storeRecord(taskID, record)

	var childRunState *RunState

	// Create child UI session if the manager is available.
	// Shared read: only the parent's BrowserID is needed to link the child.
	if s.uiSessionMgr != nil {
		parentMeta := s.uiSessionMgr.GetMetaShared(sessionID)
		if parentMeta != nil {
			title := loop.TruncateText(task, 60)
			childSess, childErr := s.uiSessionMgr.CreateChild(sessionID, parentMeta.BrowserID, title)
			if childErr != nil {
				slog.Warn("failed to create child session for sub-agent",
					slog.String("task_id", taskID),
					slog.Any("error", childErr),
				)
			} else {
				record.ChildSessionID = childSess.ID
				slog.Info("created child session for sub-agent",
					slog.String("task_id", taskID),
					slog.String("child_session_id", childSess.ID),
				)
				// Broadcast session_status so the child appears in sidebar immediately
				s.BroadcastToBrowser(parentMeta.BrowserID, BrowserEvent{
					Type: "session_status",
					Data: map[string]any{
						"session_id": childSess.ID,
						"parent_id":  sessionID,
						"status":     string(childSess.Status),
					},
				})
			}
		}
	}

	// Register child session in active runs so SSE subscribers can connect
	if record.ChildSessionID != "" {
		childRunState = &RunState{
			SessionID: record.ChildSessionID,
			Cancel:    cancel,
			StartedAt: time.Now(),
			Done:      make(chan struct{}),
			SSE:       sseState,
		}

		s.exchangeIfDone(record.ChildSessionID)
		if s.get(record.ChildSessionID) != nil {
			slog.Warn("child session already has active run", slog.String("child_session_id", record.ChildSessionID))
			cancel()
			return "", fmt.Errorf("child session %s already has an active run", record.ChildSessionID)
		}
		s.store(record.ChildSessionID, childRunState)
	}

	// Build the unified run-completer for the delegated child run. The snapshot
	// is keyed by the task ID (the same ID the sub-agent's HTTP traces are
	// recorded under) with parent linkage and a title derived from the task
	// (issue #1041). The conversation source is request-based (issue #1107).
	completer := &runCompleter{
		svc:          s,
		historyMgr:   loop.NewRequestHistoryManager(req),
		id:           taskID,
		parentID:     sessionID,
		title:        uisession.TitlePreview(task),
		systemPrompt: systemPrompt,
		workspace:    parentCfg.Workspace,
		startedAt:    record.StartedAt,
		cfg:          parentCfg,
	}
	if completer.title == "" {
		completer.title = "Sub-agent task"
	}
	if s.uiSessionMgr != nil {
		if parentMeta := s.uiSessionMgr.GetMetaShared(sessionID); parentMeta != nil {
			completer.browserID = parentMeta.BrowserID
		}
	}
	// Write the initial snapshot synchronously before the run goroutine starts
	// so sessions/<taskID>/session.json exists before any HTTP trace (recorded
	// under the same task ID) can complete — SaveTrace drops traces when no
	// session.json exists for the session directory.
	completer.persist(uisession.StatusRunning)

	go func() {
		defer func() {
			// Release browser allocator connections for this sub-agent's task ID
			toolReg.EndSession(taskID)
			record.finish()
			// Clean up child session's RunState from active runs
			if record.ChildSessionID != "" {
				s.remove(record.ChildSessionID, childRunState)
				// Update child session status to idle
				s.broadcastSessionStatusUpdate(record.ChildSessionID, uisession.StatusIdle)
			}
			// Reap after TTL
			time.AfterFunc(subAgentReapTTL, func() {
				s.subagents.reapAfterTTL(taskID)
			})
		}()

		w := runstate.NewWriter(sseState)
		historyMgr := completer.historyMgr

		runErr := loop.RunAgent(subCtx, loop.RunSpec{
			Client:     llmSvc,
			Request:    req,
			MaxTurns:   maxTurns,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      toolReg,
		}, loop.RunOpts{
			HistoryMgr:       historyMgr,
			Confirmer:        nil,
			UISessionMgr:     s.uiSessionMgr,
			SessionID:        "",
			RunID:            timeline.GenerateRunID(taskID, record.StartedAt),
			ContextWindow:    parentCfg.ContextWindowTokens,
			CrashDumpFunc:    nil,
			Turns:            nil,
			TurnCompleter:    completer,
			CalibrationStore: s.calibrationStore,
			TurnTimeout:      parentCfg.TurnTimeout,
			ModelName:        parentCfg.ModelName,
			RetryPolicy:      &parentCfg.RetryPolicy,
		})

		// Persist sub-agent response to child UI session
		if record.ChildSessionID != "" {
			content := sseState.BufferString()
			reasoningContent := sseState.ReasoningBufferString()
			if content != "" {
				s.appendToSession(record.ChildSessionID, content, reasoningContent)
			}
		}

		// Extract result from last assistant message
		record.Result, record.TurnCount = extractSubAgentResult(req.Messages)

		if runErr != nil {
			// Cancellation / context deadline — terminal snapshot is idle,
			// matching the UI path's cancelled exit.
			if subCtx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				record.Status = subAgentCancelled
				completer.terminal(sseState, runCompleterTerminalStatus(timeline.TerminationCancelled), &timeline.TimelineTermination{
					Reason:  timeline.TerminationCancelled,
					Message: "Run cancelled by user or context deadline exceeded",
				})
				slog.Info("sub-agent cancelled", slog.String("task_id", taskID))
				return
			}

			// Max turns exceeded — same terminal handling as the UI path.
			var maxTurnsErr *loop.MaxTurnsExceededError
			if errors.As(runErr, &maxTurnsErr) {
				record.Status = subAgentError
				record.Err = runErr
				completer.terminal(sseState, runCompleterTerminalStatus(timeline.TerminationMaxTurns), &timeline.TimelineTermination{
					Reason:  timeline.TerminationMaxTurns,
					Message: uixt.MaxTurnsMessage(maxTurnsErr.Limit),
				})
				slog.Warn("sub-agent max turns exceeded",
					slog.String("task_id", taskID),
					slog.Int("limit", maxTurnsErr.Limit),
				)
				return
			}

			record.Status = subAgentError
			record.Err = runErr
			completer.terminal(sseState, runCompleterTerminalStatus(timeline.TerminationError), &timeline.TimelineTermination{
				Reason:  timeline.TerminationError,
				Message: runErr.Error(),
			})
			slog.Warn("sub-agent error", slog.String("task_id", taskID), slog.Any("error", runErr))
			return
		}

		record.Status = subAgentCompleted
		completer.terminal(sseState, runCompleterTerminalStatus(timeline.TerminationCompleted), &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		})
		slog.Info("sub-agent completed",
			slog.String("task_id", taskID),
			slog.Int("turn_count", record.TurnCount),
			slog.Int("result_len", len(record.Result)),
		)
	}()

	return taskID, nil
}

// CollectSubAgents blocks until all specified tasks complete or the context is cancelled.
// Returns a map keyed by task ID with status, result, and turn_count.
func (s *RunService) CollectSubAgents(ctx context.Context, taskIDs []string) (map[string]SubAgentResult, error) {
	if len(taskIDs) == 0 {
		return map[string]SubAgentResult{}, nil
	}

	slog.Info("collecting sub-agents", slog.Int("count", len(taskIDs)))

	// Gather all done channels under lock
	type recordInfo struct {
		done   chan struct{}
		record *subAgentRecord
	}
	recordsMap, err := s.subagents.getRecords(taskIDs)
	if err != nil {
		return nil, err
	}
	records := make([]recordInfo, 0, len(taskIDs))
	for _, rec := range recordsMap {
		records = append(records, recordInfo{done: rec.Done, record: rec})
	}

	// Wait for each task to complete, snapshotting each record's final result
	// at the moment its done channel closes. Completed records are reaped from
	// the store after subAgentReapTTL, so a long wait for a slow task would
	// otherwise lose the results of fast tasks — they came back as "cancelled"
	// with empty results even though they completed. The record pointer stays
	// valid after reaping; only the store entry is removed.
	results := make(map[string]SubAgentResult, len(taskIDs))
	for _, ri := range records {
		select {
		case <-ri.done:
			// Task completed — snapshot now, before the record can be reaped.
			results[ri.record.TaskID] = subAgentRecordToResult(ri.record)
		case <-ctx.Done():
			// Context cancelled — return partial results: prefer the snapshot
			// for tasks already observed done, then fall back to the store.
			slog.Info("collect cancelled, returning partial results")
			for _, tid := range taskIDs {
				if _, ok := results[tid]; ok {
					continue
				}
				if rec := s.subagents.getRecord(tid); rec != nil {
					results[tid] = subAgentRecordToResult(rec)
				} else {
					results[tid] = SubAgentResult{Status: "cancelled"}
				}
			}
			return results, nil
		}
	}

	return results, nil
}

// extractSubAgentResult extracts the final result content and turn count
// from a sub-agent's message history (litellm format). It picks the content
// of the LAST assistant message and counts both text-producing and
// tool-calling turns.
func extractSubAgentResult(msgs []litellm.Message) (result string, turnCount int) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == litellm.Role("assistant") {
			// Extract text content from blocks
			textContent := extractTextFromBlocks(msgs[i].Blocks)
			result = textContent
			if textContent != "" {
				turnCount++
			}
			break // Use the LAST assistant message; don't overwrite with earlier ones
		}
	}
	// Count tool-calling turns
	for _, msg := range msgs {
		if msg.Role == litellm.Role("assistant") && hasToolUseBlocks(msg.Blocks) {
			turnCount++
		}
	}
	return result, turnCount
}

// extractTextFromBlocks concatenates all TextBlock content from a block slice.
func extractTextFromBlocks(blocks []litellm.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		if text, ok := block.(litellm.TextBlock); ok {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// hasToolUseBlocks returns true if the block slice contains any ToolUseBlock.
func hasToolUseBlocks(blocks []litellm.Block) bool {
	for _, block := range blocks {
		if _, ok := block.(litellm.ToolUseBlock); ok {
			return true
		}
	}
	return false
}

// subAgentRecordToResult converts an internal record to the public result type.
func subAgentRecordToResult(rec *subAgentRecord) SubAgentResult {
	status := string(rec.Status)
	if rec.Status == subAgentRunning {
		// If task hasn't finished yet (shouldn't happen if properly awaited)
		status = "cancelled"
	}
	return SubAgentResult{
		Status:    status,
		Result:    rec.Result,
		TurnCount: rec.TurnCount,
	}
}

// CancelSubAgents cancels all in-flight sub-agents for a given parent session.
func (s *RunService) CancelSubAgents(sessionID string) {
	s.subagents.CancelForSession(sessionID)
}

// buildBaseToolRegistry creates a tool registry with the standard tools plus
// the skill tool (when a skills service is wired) — the leaf/delegated toolset
// (issue #1092). delegate, collect, and render_quick_replies are parent-only /
// UI-only and are registered by the run-preparation seam only when
// allowDelegate is true (render_quick_replies additionally only when a UI
// session exists), so a delegated run never gains them.
func buildBaseToolRegistry(cfg RunConfig, skillDirs []string, skillsSvc *skills.Service, uiSessionMgr *uisession.Manager) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(tool.NewBashTool(cfg.Workspace, cfg.CmdTimeout, cfg.Sandbox))
	reg.Register(tool.NewGrepTool(cfg.Workspace))
	reg.Register(tool.NewReadTool(cfg.Workspace, skillDirs, cfg.AllowedReadPaths))
	reg.Register(tool.NewWriteTool(cfg.Workspace))
	reg.Register(tool.NewEditTool(cfg.Workspace))
	reg.Register(tool.NewRenderMermaidDiagram())
	reg.Register(tool.NewWebFetchTool())
	reg.Register(tool.NewBrowserTool(cfg.BrowserWsUrl, cfg.Workspace))
	if skillsSvc != nil {
		reg.Register(tool.NewSkill(skillsSvc, uiSessionMgr))
	}
	return reg
}
