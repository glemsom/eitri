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
//     (defined in runconfig sub-package)
//   - RunState — one active run's SSE state, cancel func, and completion signal
//   - BrowserEvent — event sent to browser-level SSE subscribers
//     (defined in broadcast sub-package)
//
// # Sub-package hierarchy
//
//	runner/            — RunService wiring, RunState, run tracking,
//	                     batch mode, sub-agent orchestration,
//	                     system prompt assembly, skill context
//	├── runconfig/     — RunConfig type, FromConfig builder,
//	│                    MaxTurnsExceededError
//	├── broadcast/     — BrowserBroadcaster, BrowserEvent types
//	├── adapters/      — HistoryManager and Confirmer interfaces +
//	│                    implementations (sessionHistoryManager,
//	│                    requestHistoryManager, testConfirmerStub,
//	│                    funcConfirmer), ConfirmationResult,
//	│                    ConfirmationFunc value types
//	└── loop/          — RunAgent, RunSpec, RunOpts, the agent turn
//	                     loop, streaming, tool dispatch, message
//	                     trimming, and LLM error handling
//
// # Responsibilities by file
//
//	service.go       — RunService type, constructor, subscribe/unsubscribe,
//	                   cancel, confirm path, browser SSE broadcast
//	run.go           — StartRun (agent loop entry point), tool registry
//	                   assembly, session persistence after run
//	batch.go         — BatchRun: headless batch mode (no UI sessions,
//	                   sessionHistoryManager, io.Writer output)
//	system_prompt.go — buildSystemPrompt and buildLLMService: shared
//	                   helpers used by run.go, batch.go, and subagent.go.
//	                   buildLLMService assembles auth, LLM service, tool
//	                   registry, and the system prompt in one seam call.
//	subagent.go      — SpawnSubAgent, CollectSubAgents, CancelSubAgents,
//	                   sub-agent record tracking, restricted tool registry
//	skill_context.go — sessionSkillContext resolution, stale skill
//	                   detection, skill directory enumeration
//	repo_instructions.go — readRepositoryInstructions (AGENTS.md loader)
//	run_tracker.go   — Concurrency-safe active-run map, cancel, snapshot
//	subagent_store.go — In-flight sub-agent record store and parent config
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
//   - config    — (transitive through runconfig/) Config value object
//
// And from its own sub-packages:
//
//   - runner/runconfig  — RunConfig, MaxTurnsExceededError
//   - runner/broadcast  — BrowserBroadcaster, BrowserEvent
//   - runner/adapters   — HistoryManager, Confirmer, ConfirmationResult
//   - runner/loop       — RunAgent, RunSpec, RunOpts
//
// # Extension points
//
//  1. Adding a new agent run lifecycle hook:
//     Add a pre/post hook call in startRunWithConfig (run.go) or RunAgent
//     (loop/loop.go). Hooks have access to RunConfig, *litellm.Request, and
//     *runstate.State.
//
//  2. Adding a new HistoryManager adapter:
//     Implement the HistoryManager interface (adapters package) and construct it
//     in the adapter factory section of startRunWithConfig (run.go).
//
//  3. Adding a new Confirmer adapter:
//     Implement the Confirmer interface (adapters package). The production
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
