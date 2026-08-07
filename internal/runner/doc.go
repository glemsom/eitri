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
//	prepare.go       — prepareRun: the unified run-preparation seam shared
//	                   by run.go, batch.go, and subagent.go (ADR-0024). Takes
//	                   an allow_delegate option: UI/batch parents set it true
//	                   (registering delegate/collect, plus render_quick_replies
//	                   when a UI session exists); delegated runs set it false
//	                   (leaf toolset, no recursion). Produces the tool
//	                   registry, *litellm.Request, and the system prompt in
//	                   one parameterized call; buildRunRequest is shared with
//	                   all run kinds.
//	compact.go       — autoCompactAfterTurn: the shared auto-compaction step
//	                   for UI, batch, and sub-agent runs (issues #1093, #1096);
//	                   replaces the run's history with the compacted version
//	                   via the history manager's replace-history capability
//	run.go           — StartRun (agent loop entry point), session persistence
//	                   after run, and the UI transport's per-reason exit work
//	                   (uiExitWork: run-end append, SSE closing events, crash
//	                   dump, session-status broadcast). Terminal status
//	                   snapshot + timeline writes live in the run-end seam
//	                   (run_exit.go), not here.
//	batch.go         — BatchRun: headless batch mode (no UI sessions,
//	                   loop.NewSessionHistoryManager, io.Writer output)
//	batch_persist.go — Batch-run title derivation (batchTitle). Batch session
//	                   persistence itself lives in the unified run-completer
//	                   (run_completer.go): per-turn snapshots, per-call HTTP
//	                   traces, and the per-run timeline.
//	run_completer.go — runCompleter: the unified per-turn run-completer for
//	                   UI, batch, and sub-agent runs (issues #1107, #1201,
//	                   ADR-0028); snapshot source parameterized per transport
//	                   (UI: live-sync + CopySession; batch/sub-agent:
//	                   buildUISession from history). Its terminal seam persists
//	                   the run timeline under the run ID plumbed in at
//	                   construction (runID, generated once per run at run
//	                   start — issue #1234), never a recomputed ID.
//	run_exit.go      — the single run-end terminal seam shared by the UI,
//	                   batch, and sub-agent transports (issue #1238,
//	                   ADR-0028/0029): exit classification (classifyRunExit),
//	                   the single per-reason exit switch (exitWork), and the
//	                   terminal snapshot + timeline write (runExit →
//	                   runCompleter.terminal). runExit is directly callable,
//	                   so exit paths are unit-testable without a full run.
//	system_prompt.go — buildSystemPrompt and buildLLMService: shared
//	                   helpers used by the prepareRun seam (prepare.go).
//	                   buildLLMService assembles auth, LLM service, tool
//	                   registry, and the system prompt in one call.
//	subagent.go      — SpawnSubAgent, CollectSubAgents, CancelSubAgents,
//	                   sub-agent record tracking, restricted tool registry,
//	                   per-turn child-session snapshots, sub-agent
//	                   auto-compaction (via the shared autoCompactAfterTurn
//	                   step, issue #1096), and the sub-agent transport's
//	                   per-reason exit work (subagentExitWork)
//	skill_context.go — sessionSkillContext resolution, stale skill
//	                   detection, skill directory enumeration
//	repo_instructions.go — readRepositoryInstructions (AGENTS.md loader)
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
