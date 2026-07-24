// Package runner provides the run lifecycle seam — it owns agent loop execution,
// SSE broadcast, session persistence, auth refresh callbacks, and sub-agent
// orchestration.
//
// RunService is the central type. It manages active runs, SSE subscriber
// fan-out, confirmation channels, sub-agent lifecycle, and browser-level
// cross-session event broadcast.
//
// # Key types
//
//   - RunService — run lifecycle manager (start, cancel, subscribe, confirm)
//   - RunConfig — per-run configuration (provider, model, system prompt, turns)
//   - RunSpec — transport/config fields for RunAgent (LLM service, request, tools, SSE writer, caps)
//   - AgentConfig — runtime/UI configuration for RunAgent (history, confirmer, session, etc.)
//   - RunState — one active run's SSE state, cancel func, and completion signal
//   - ConfirmationResult — user decision (approved/denied) for a path confirmation
//   - ConfirmationFunc — callback signature for confirmation prompts
//   - MaxTurnsExceededError — error returned when the agent hits the turn cap
//   - BrowserEvent — event sent to browser-level SSE subscribers
//
// # Key interfaces
//
//   - HistoryManager — abstracts conversation history storage (read + append).
//     Two adapters exist: sessionHistoryManager (browser UI path via
//     *history.SessionManager) and requestHistoryManager (headless/direct-messages
//     path via *litellm.Request). Defined in interfaces.go.
//   - Confirmer — abstracts user-confirmation flow. The production implementation
//     uses a channel-based mechanism (RunService.confirmPath). testConfirmerStub
//     provides canned results for unit tests. Defined in interfaces.go.
//
// # File map
//
//	service.go         — RunService type, constructor, subscribe/unsubscribe,
//	                     cancel, confirm path, browser SSE broadcast
//	run.go             — StartRun (agent loop entry point), tool registry assembly,
//	                     session persistence after run
//	loop.go            — RunAgent: synchronous turn loop, LLM call, tool dispatch,
//	                     SSE broadcast, context window estimation, streaming
//	loop_helpers.go    — message trimming, content truncation, XML tag parsing,
//	                     SSE event helpers, context window computation
//	batch.go           — BatchRun: headless batch mode (no UI sessions,
//	                     sessionHistoryManager, io.Writer output)
//	system_prompt.go   — buildSystemPrompt and buildLLMService: shared helpers
//	                     used by run.go, batch.go, and subagent.go.
//	                     buildLLMService assembles auth, LLM service, tool
//	                     registry, AND the system prompt in one seam call.
//	subagent.go        — SpawnSubAgent, CollectSubAgents, CancelSubAgents,
//	                     sub-agent record tracking, restricted tool registry
//	skill_context.go   — sessionSkillContext resolution, stale skill detection,
//	                     skill directory enumeration
//	interfaces.go      — HistoryManager and Confirmer contracts
//	adapters.go        — sessionHistoryManager, requestHistoryManager,
//	                     testConfirmerStub, funcConfirmer implementations
//	repo_instructions.go — readRepositoryInstructions (AGENTS.md loader)
//	runconfig.go       — RunConfig type, FromConfig builder
//
// # Dependencies
//
// This package imports from the following internal packages:
//
//   - litellm   — LLM transport abstraction (LLMService, AdapterConfig, Request)
//   - tool      — ToolHandler, Registry, built-in tools (bash, read, write, etc.)
//   - runstate  — SSE event types (SSEEvent, RenderKind), State, Writer
//   - session   — UI session Manager (uisession)
//   - history   — session history Manager and default system prompt
//   - provider  — auth resolution, provider descriptions
//   - skills    — Skill discovery, activation, resource manifests
//   - debug     — HTTP trace recorder (optional)
//   - config    — (transitive through runconfig.go) Config value object
//
// # Extension points
//
//  1. Adding a new agent run lifecycle hook:
//     Add a pre/post hook call in startRunWithConfig (run.go) or RunAgent
//     (loop.go). Hooks have access to RunConfig, *litellm.Request, and
//     *runstate.State.
//
//  2. Adding a new HistoryManager adapter:
//     Implement the HistoryManager interface (interfaces.go) and construct it
//     in the adapter factory section of startRunWithConfig (run.go).
//
//  3. Adding a new Confirmer adapter:
//     Implement the Confirmer interface (interfaces.go). The production
//     implementation uses channel-based confirmation via ResolveConfirmation;
//     alternative adapters could use webhook calls or file-system signals.
//
//  4. Adding a new built-in tool available to the agent:
//     Create the tool in internal/tool/ (implementing tool.Handler), then
//     register it via toolReg.Register(...) in buildBaseToolRegistry
//     (subagent.go) for all runs, or in startRunWithConfig for parent-only tools.
//
//  5. Modifying batch mode behaviour:
//     BatchRun (batch.go) mirrors startRunWithConfig but uses
//     requestHistoryManager, denies confirmations, and streams tokens to an
//     io.Writer. Keep it in sync when adding new lifecycle features.
package runner
