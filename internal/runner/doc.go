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
//   - RunState — one active run's SSE state, cancel func, and completion signal
//   - BrowserEvent — event sent to browser-level SSE subscribers
//     (defined in this package)
//
// # Responsibilities by file
//
//	service.go       — RunService type, constructor, subscribe/unsubscribe,
//	                   cancel, confirm path, browser SSE broadcast
//	prepare.go       — prepareRun: the unified parent-run preparation seam
//	                   shared by run.go and batch.go (ADR-0024). Produces the
//	                   tool registry, *litellm.Request, and system prompt in
//	                   one parameterized call; buildRunRequest is shared with
//	                   sub-agent runs too.
//	compact.go       — autoCompactAfterTurn: the shared auto-compaction step
//	                   for UI, batch, and sub-agent runs (issues #1093, #1096);
//	                   replaces the run's history with the compacted version
//	                   via the history manager's replace-history capability
//	run.go           — StartRun (agent loop entry point), session persistence
//	                   after run, UI OnTurnComplete (snapshot + compaction)
//	batch.go         — BatchRun: headless batch mode (no UI sessions,
//	                   loop.NewSessionHistoryManager, io.Writer output)
//	batch_persist.go — Batch session persistence: per-turn snapshots and the
//	                   batch turn completer (snapshot + shared compaction)
//	system_prompt.go — buildSystemPrompt and buildLLMService: shared
//	                   helpers used by run.go, batch.go, and subagent.go.
//	                   buildLLMService assembles auth, LLM service, tool
//	                   registry, and the system prompt in one seam call.
//	subagent.go      — SpawnSubAgent, CollectSubAgents, CancelSubAgents,
//	                   sub-agent record tracking, restricted tool registry,
//	                   per-turn child-session snapshots, and sub-agent
//	                   auto-compaction (via the shared autoCompactAfterTurn
//	                   step, issue #1096)
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
//   - litellm   — LLM transport abstraction (*litellm.Client, Request)
//   - tool      — ToolHandler, Registry, built-in tools (bash, read, write, etc.)
//   - runstate  — SSE event types (SSEEvent, RenderKind), State, Writer
//   - session   — UI session Manager (uisession)
//   - history   — session history Manager and default system prompt
//   - provider  — auth resolution, provider descriptions
//   - skills    — Skill discovery, activation, resource manifests
//   - debug     — HTTP trace recorder (optional)
//   - config    — Config value object
//   - runner/loop — RunAgent, RunSpec, RunOpts, HistoryManager, Confirmer,
//     ConfirmationResult
//
// # Extension points
//
//  1. Adding a new agent run lifecycle hook:
//     Add a pre/post hook call in startRunWithConfig (run.go) or RunAgent
//     (loop/loop.go). Hooks have access to RunConfig, *litellm.Request, and
//     *runstate.State.
//
//  2. Adding a new HistoryManager adapter:
//     Implement the HistoryManager interface (loop package) and construct it
//     in the adapter factory section of startRunWithConfig (run.go).
//
//  3. Adding a new Confirmer adapter:
//     Implement the Confirmer interface (loop package). The production
//     implementation uses channel-based confirmation via ResolveConfirmation;
//     alternative adapters could use webhook calls or file-system signals.
//
//  4. Adding a new built-in tool available to the agent:
//     Create the tool in internal/tool/ (implementing tool.Handler), then
//     register it via toolReg.Register(...) in buildBaseToolRegistry
//     (subagent.go) for all runs, or in prepareRun (prepare.go) for
//     parent-only tools.
//
//  5. Modifying batch mode behaviour:
//     BatchRun (batch.go) shares run preparation with the UI via prepareRun
//     (prepare.go) and differs only in genuinely mode-specific runtime
//     wiring: no UI session, no confirmation prompt (auto-denied), and
//     text-to-stdout instead of SSE streaming. Add mode-specific knobs to
//     runPrepOptions rather than patching one path.
package runner
