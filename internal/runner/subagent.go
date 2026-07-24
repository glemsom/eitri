package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tool"
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

// SpawnSubAgent starts a sub-agent in the background to complete the given task.
// Returns a unique task ID immediately. The sub-agent runs with its own LLM
// service, tool registry (restricted — no delegate/collect/quick_replies/skill),
// and request-based history manager (no browser session persistence).
//
// Cancelling the parent run cascades to cancel all in-flight sub-agents.
func (s *RunService) SpawnSubAgent(ctx context.Context, sessionID, task string, maxTurns int) (taskID string, err error) {
	// Retrieve parent config for this session
	parentCfg, ok := s.subagents.GetParentCfg(sessionID)
	if !ok {
		return "", fmt.Errorf("no parent run config found for session %s", sessionID)
	}

	// Generate task ID
	taskID = s.subagents.nextID()

	slog.Info("spawning sub-agent",
		slog.String("task_id", taskID),
		slog.String("parent_session", sessionID),
		slog.String("task", truncateText(task, 100)),
		slog.Int("max_turns", maxTurns),
	)

	// Build LLM service, tool registry, and system prompt (same provider/model as parent, restricted tools)
	llmSvc, toolReg, basePrompt, err := buildLLMService(ctx, parentCfg, taskID, nil, s.persistAuth, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr, sessionSkillContext{})
	if err != nil {
		return "", fmt.Errorf("sub-agent LLM service: %w", err)
	}

	// Append task-specific suffix to the base system prompt
	systemPrompt := basePrompt + "\n\nYou are performing the following task: " + task

	// Create request and set up messages
	req := &llm.Request{
		Model:  parentCfg.ModelName,
		Stream: true,
	}
	// Set task ID as prompt cache key if the provider supports it
	providerDesc, _ := provider.Describe(parentCfg.ProviderID)
	if providerDesc.SupportsPromptCache {
		req.SessionID = taskID
	}

	if parentCfg.ThinkingLevel != "" {
		req.ReasoningEffort = parentCfg.ThinkingLevel
	}
	req.Messages = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}

	sseState := runstate.New()
	subCtx, cancel := context.WithCancel(ctx)

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

	// Create child UI session if the manager is available
	if s.uiSessionMgr != nil {
		parentSess := s.uiSessionMgr.Get(sessionID)
		if parentSess != nil {
			title := truncateText(task, 60)
			childSess, childErr := s.uiSessionMgr.CreateChild(sessionID, parentSess.BrowserID, title)
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
				s.BroadcastToBrowser(parentSess.BrowserID, BrowserEvent{
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

		s.tracker.exchangeIfDone(record.ChildSessionID)
		if s.tracker.get(record.ChildSessionID) != nil {
			slog.Warn("child session already has active run", slog.String("child_session_id", record.ChildSessionID))
			cancel()
			return "", fmt.Errorf("child session %s already has an active run", record.ChildSessionID)
		}
		s.tracker.store(record.ChildSessionID, childRunState)
	}

	go func() {
		defer func() {
			record.finish()
			// Clean up child session's RunState from active runs
			if record.ChildSessionID != "" {
				s.tracker.remove(record.ChildSessionID, childRunState)
				// Update child session status to idle
				s.broadcastSessionStatusUpdate(record.ChildSessionID, uisession.StatusIdle)
			}
			// Reap after TTL
			time.AfterFunc(subAgentReapTTL, func() {
				s.subagents.reapAfterTTL(taskID)
			})
		}()

		w := runstate.NewWriter(sseState)
		historyMgr := newRequestHistoryManager(req)

		runErr := RunAgent(subCtx, AgentConfig{
			Service:       llmSvc,
			Request:       req,
			MaxTurns:      maxTurns,
			MaxHistory:    0,
			SSEWriter:     w,
			Tools:         toolReg,
			HistoryMgr:    historyMgr,
			Confirmer:     nil,
			UISessionMgr:  s.uiSessionMgr,
			SessionID:     "",
			ContextWindow: 0,
			CrashDumpFunc: nil,
			Turns:         nil,
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
		msgs := req.Messages
		var result string
		var turnCount int
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "assistant" {
				result = msgs[i].Content
				// Count assistant messages with content (tool-calling turns + final)
				if msgs[i].Content != "" {
					turnCount++
				}
			}
		}
		// Count tool-calling turns
		for _, msg := range msgs {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				turnCount++
			}
		}

		record.Result = result
		record.TurnCount = turnCount

		if runErr != nil {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				record.Status = subAgentCancelled
				slog.Info("sub-agent cancelled", slog.String("task_id", taskID))
			} else {
				record.Status = subAgentError
				record.Err = runErr
				slog.Warn("sub-agent error", slog.String("task_id", taskID), slog.Any("error", runErr))
			}
			return
		}

		record.Status = subAgentCompleted
		slog.Info("sub-agent completed",
			slog.String("task_id", taskID),
			slog.Int("turn_count", turnCount),
			slog.Int("result_len", len(result)),
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

	// Wait for each task to complete
	for _, ri := range records {
		select {
		case <-ri.done:
			// Task completed
		case <-ctx.Done():
			// Context cancelled — return partial results
			slog.Info("collect cancelled, returning partial results")
			results := make(map[string]SubAgentResult, len(taskIDs))
			for _, tid := range taskIDs {
				rec := s.subagents.getRecord(tid)
				if rec == nil {
					results[tid] = SubAgentResult{
						Status: "cancelled",
					}
					continue
				}
				results[tid] = subAgentRecordToResult(rec)
			}
			return results, nil
		}
	}

	// Collect results
	results := make(map[string]SubAgentResult, len(taskIDs))
	for _, tid := range taskIDs {
		rec := s.subagents.getRecord(tid)
		if rec == nil {
			results[tid] = SubAgentResult{Status: "cancelled"}
			continue
		}
		results[tid] = subAgentRecordToResult(rec)
	}

	return results, nil
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

// buildBaseToolRegistry creates a tool registry with all standard tools
// except delegate, collect, render_quick_replies, and skill (which are
// only available to parent agents, not sub-agents).
func buildBaseToolRegistry(cfg RunConfig, skillDirs []string, skillsSvc *skills.Service, uiSessionMgr *uisession.Manager) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(tool.NewBashTool(cfg.Workspace, cfg.CmdTimeout))
	reg.Register(tool.NewGlobTool(cfg.Workspace))
	reg.Register(tool.NewGrepTool(cfg.Workspace))
	reg.Register(tool.NewReadTool(cfg.Workspace, skillDirs, cfg.AllowedReadPaths))
	reg.Register(tool.NewWriteTool(cfg.Workspace))
	reg.Register(tool.NewEditTool(cfg.Workspace))
	reg.Register(tool.NewRenderMermaidDiagram())
	reg.Register(tool.NewWebFetchTool())
	return reg
}
