// Package engine drives a single agent run turn over the provider seam.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/skillindex"
)

var ErrMaxTurns = errors.New("maximum turn limit reached")

// ErrStopped is the dedicated stop sentinel: it wraps context.Canceled so a caller distinguishes a user-stopped turn (esc in the TUI) from a failure with errors.Is(err, context.Canceled), while the wrapped cause keeps the sentinel from matching unrelated errors.
var ErrStopped = fmt.Errorf("turn stopped: %w", context.Canceled)

// ErrStreamEOF is returned when a provider stream ends with EOF before it
// delivers a terminal signal (a done chunk, a non-empty finish_reason, or tool
// calls). That is a truncated/aborted response, not a clean completion.
var ErrStreamEOF = errors.New("provider stream ended without a complete response; the connection was closed mid-stream")

// TranscriptWriter records the run's on-disk trail.
type TranscriptWriter interface {
	WriteTranscript(line []byte) error
}

// RunEndSink receives the terminal reason of one run, called exactly once per
// run. The reason is one of the provider.RunEnd* values.
type RunEndSink interface {
	LogRunEnd(reason string)
}

// TranscriptErrorReporter receives the number of transcript writes that failed
// during one run. RunAgent invokes it at most once per run, and only when at
// least one write failed, so a broken trail is surfaced without per-write noise.
type TranscriptErrorReporter func(failed int)

type Engine struct {
	provider   provider.Provider
	transcript TranscriptWriter
	runEnd     RunEndSink
	onTrErr    TranscriptErrorReporter
	listener   Listener

	histMu    sync.Mutex
	histories map[string][]provider.Message

	runMu   sync.Mutex
	nextRun int
}

func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr, histories: make(map[string][]provider.Message)}
}

func (e *Engine) SetTranscript(tr TranscriptWriter) {
	e.transcript = tr
}

func (e *Engine) SetRunEndSink(s RunEndSink) {
	e.runEnd = s
}

func (e *Engine) SetTranscriptErrorReporter(r TranscriptErrorReporter) {
	e.onTrErr = r
}

// Listener receives one typed Event per streamed observation from a live run, in order, synchronously from within the turn's drain loop.
type Listener func(Event)

func (e *Engine) SetListener(l Listener) {
	e.listener = l
}

func (e *Engine) emit(evt Event) {
	if e.listener != nil {
		e.listener(evt)
	}
}

func (e *Engine) claimRunID() int {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	e.nextRun++
	return e.nextRun
}

// RunRequest is a single non-tool turn of work.
type RunRequest struct {
	Model  string
	Prompt string

	SkillInject *string
	SessionKey  string

	// Workspace states the host-absolute cwd the session operates in. Unlike the
	// byte-stable system prompt, it is per-run state, so it rides as its own
	// system-layer message. It is appended after every static system message
	// (persona head, skill index, repo instructions) so the request's static
	// prefix stays byte-identical across runs and sessions: a provider cache
	// breakpoint placed at the end of that prefix then keeps hitting, which a
	// directive wedged between static blocks would break. Empty omits it.
	Workspace string

	// WritablePaths names the paths this run may write, in the order the
	// sandbox binds them: the workspace, the session temp, then each configured
	// extra-writable path. Like Workspace it is per-run state, so it rides in
	// the same system-layer directive rather than the byte-stable system prompt.
	// Empty omits the write-permissions section entirely, which is what an
	// unsandboxed (--yolo-unsafe) run reports: it has no boundary to state, and
	// the bash tool description is what already declares the absent sandbox.
	WritablePaths []string

	// SkillIndex is an optional pre-rendered model-visible skill inventory. When
	// set, it is carried to the provider as a dedicated system-layer message
	// appended after the persona head so the model sees available skills without
	// perturbing the byte-stable system prompt. Nil omits the message entirely,
	// keeping the outgoing request byte-identical to the no-index case.
	SkillIndex *string

	// RepoInstructions is the optional content of the workspace-root AGENTS.md.
	// When set, it is carried to the provider as a dedicated system-layer message
	// appended after the persona head and any skill index — and before the
	// workspace directive, which is per-run state — so repository-authored
	// instructions reach the model without perturbing the byte-stable system
	// prompt. Nil omits the message entirely, keeping the outgoing request
	// byte-identical to the pre-feature case.
	RepoInstructions *string

	ThinkingEnabled bool
	ReasoningEffort string

	// ProviderID is the provider family this run targets, chosen by config, so
	// the shared dialect can apply provider-specific wire fields.
	ProviderID provider.ProviderID
}

type Result struct {
	Answer    string
	Reasoning string

	// Usage is the run's accumulated token usage, summed over every turn. Nil
	// when the provider reported no usage for any turn.
	Usage *provider.Usage

	// Turns is how many provider request/response pairs the run performed, tool-calling turns included.
	Turns int

	// Stopped reports whether the run ended in a user stop (ErrStopped) rather
	// than a normal completion or a failure.
	Stopped bool

	// Model is the model that answered the run, empty when none was configured.
	Model string

	// DurationMs is the run's own wall clock in milliseconds, measured from the
	// start of RunAgent to its final answer.
	DurationMs int64

	// ToolCalls is how many tool calls the run dispatched and executed; calls
	// abandoned because the run stopped mid-dispatch are not counted.
	ToolCalls int
}

// systemPromptHead returns the byte-stable embedded Eitri system prompt as the
// immutable request-head message.
func systemPromptHead() []provider.Message {
	return []provider.Message{{Role: provider.RoleSystem, Content: SystemPromptContent()}}
}

// workspaceDirective renders the per-run working-directory statement as its own
// system-layer directive, followed by the write-permissions statement when the
// run is filesystem-confined. Both are dynamic state (unlike the byte-stable
// system prompt), so they are generated at request-build time from the live
// workspace and registry rather than baked into prompt.md.
func workspaceDirective(workspace string, writable []string) string {
	d := "## Working directory\nYou are operating in the workspace `" + workspace + "`. Every bash call is a fresh shell already started there, so relative paths resolve against it without a `cd`."
	if len(writable) == 0 {
		return d
	}
	paths := make([]string, len(writable))
	for i, p := range writable {
		paths[i] = "`" + p + "`"
	}
	return d + "\n\n## Write permissions\nWrites are confined to " +
		strings.Join(paths, ", ") + ". Every other path is read-only."
}

// repoInstructionsDirective renders the workspace-root AGENTS.md content as a
// dedicated system-layer message head: the heading anchors the matcher that
// strips it from persisted history and preserves it through compaction, so the
// injected block is never duplicated on a later turn.
func repoInstructionsDirective(content string) string {
	return "## Repository instructions (AGENTS.md)\n\n" + content
}

// bindSkillToPrompt keeps an explicitly user-selected skill adjacent to the
// request in the user layer, subject to higher-priority instructions.
func bindSkillToPrompt(prompt, skill string) string {
	var b strings.Builder
	b.WriteString("The user explicitly selected this skill. This binding applies its instructions to the user request, subject to higher-priority instructions.\n\n")
	b.WriteString(skill)
	b.WriteString("\n\nUser request:\n")
	b.WriteString(prompt)
	return b.String()
}

// stopped reports whether the caller's context was canceled, the condition that turns a stream/tool error into a user stop rather than a failure.
func (e *Engine) stopped(ctx context.Context) bool {
	return ctx.Err() != nil
}

// runTranscript wraps the run's transcript writer to count failed writes, so a
// broken trail is reported once at run end rather than dropped per write.
type runTranscript struct {
	writer TranscriptWriter
	failed int
}

func (t *runTranscript) write(line []byte) {
	if t.writer == nil {
		return
	}
	if err := t.writer.WriteTranscript(line); err != nil {
		t.failed++
	}
}

// finishStopped emits the turn-ending event and the transcript record naming the
// user stop for a run aborted by cancellation.
func (e *Engine) finishStopped(tr *runTranscript, res Result, prompt string, runID, turn int) {
	e.emit(TurnEvent{RunID: runID, Turn: turn, EndReason: "stopped"})
	tr.write(fmt.Appendf(nil, "=== %s ===\n%s\n[stopped] run ended by user stop\n", prompt, res.Answer))
}

// finishMaxTurns records a run that exhausted its turn budget: the loop kept
// calling tools past the cap and the continuation hook, if any, refused a fresh
// budget.
func (e *Engine) finishMaxTurns(tr *runTranscript, res Result, prompt string, maxTurns int) {
	tr.write(fmt.Appendf(nil, "=== %s ===\n%s\n[max_turns] reached the turn cap of %d\n", prompt, res.Answer, maxTurns))
}

// runEndReason maps a run's terminal error to its provider.RunEnd* reason.
func runEndReason(err error) string {
	switch {
	case err == nil:
		return provider.RunEndCompleted
	case errors.Is(err, ErrStopped):
		return provider.RunEndStopped
	case errors.Is(err, ErrMaxTurns):
		return provider.RunEndMaxTurns
	default:
		return provider.RunEndError
	}
}

type ToolExecResult struct {
	Text       string
	Compressed bool
	Dropped    int

	// BytesDropped is the count of bytes an upstream memory bound (the sandbox
	// buffer) already rejected from the result's stream; the engine's byte cap
	// folds it into the single authoritative truncation marker.
	BytesDropped int

	// Uncapped exempts the result from the byte cap; see compress.ToolResult.
	Uncapped bool
}

// ToolExecutor executes an agent tool call.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, argsJSON string) (ToolExecResult, error)
}

// ExecutorFunc adapts a plain function to the ToolExecutor interface.
type ExecutorFunc func(ctx context.Context, name string, argsJSON string) (ToolExecResult, error)

func (f ExecutorFunc) Execute(ctx context.Context, name, argsJSON string) (ToolExecResult, error) {
	return f(ctx, name, argsJSON)
}

// AgentOptions configures the tool-call dispatch loop.
type AgentOptions struct {
	Tools                 []provider.Tool
	ToolChoice            any
	ToolSchemaEnforcement bool
	Executor              ToolExecutor
	MaxTurns              int

	CanContinue func() bool

	Compaction  *CompactionConfig
	OnCompacted func()

	lastUsage *provider.Usage
}

func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (final Result, runErr error) {
	if len(opts.Tools) > 0 && opts.Executor == nil {
		return Result{}, errors.New("declared tools require a tool executor")
	}
	runID := e.claimRunID()
	start := time.Now()
	tr := &runTranscript{writer: e.transcript}
	defer func() {
		final.Model = req.Model
		final.DurationMs = time.Since(start).Milliseconds()
		if e.runEnd != nil {
			e.runEnd.LogRunEnd(runEndReason(runErr))
		}
		if tr.failed > 0 && e.onTrErr != nil {
			e.onTrErr(tr.failed)
		}
	}()
	if ctx.Err() != nil {
		stopped := true
		defer func() { final.Stopped = stopped }()
		return final, ErrStopped
	}
	// Static system layer first, per-run state last: every message before the
	// workspace directive is byte-identical across runs, so a provider cache
	// breakpoint at the end of that block keeps reading across turns and
	// sessions. staticPrefixLen carries that boundary to the provider seam.
	messages := systemPromptHead()
	if req.SkillIndex != nil {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: *req.SkillIndex})
	}
	if req.RepoInstructions != nil {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: repoInstructionsDirective(*req.RepoInstructions)})
	}
	staticPrefixLen := len(messages)
	if req.Workspace != "" {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: workspaceDirective(req.Workspace, req.WritablePaths)})
	}
	messages = append(messages, e.sessionHistory(req.SessionKey)...)
	userContent := req.Prompt
	if req.SkillInject != nil {
		userContent = bindSkillToPrompt(userContent, *req.SkillInject)
	}
	messages = append(messages, provider.Message{Role: provider.RoleUser, Content: userContent})
	var (
		stopContent   string
		stopReasoning string
	)
	totalTurns := 0
	totalToolCalls := 0
	stopped := false
	var runUsage *provider.Usage
	defer func() {
		final.Turns = totalTurns
		final.ToolCalls = totalToolCalls
		final.Stopped = stopped
		final.Usage = runUsage
	}()

	enforceSchema := false
	if opts.ToolSchemaEnforcement {
		honored, err := provider.NegotiateGenerationControls(ctx, e.provider, []provider.ControlRequirement{
			{Control: provider.GenerationControlToolSchemaEnforcement, Required: false},
		})
		if err != nil {
			return final, err
		}
		for _, c := range honored {
			if c == provider.GenerationControlToolSchemaEnforcement {
				enforceSchema = true
			}
		}
	}

	recoveredContextOverflow := false
	for turn := 0; ; turn++ {
		totalTurns++
		var content, reasoning strings.Builder
		var turnUsage *provider.Usage
		if ctx.Err() != nil {
			stopped = true
			final.Answer = stopContent
			final.Reasoning = stopReasoning
			e.finishStopped(tr, final, req.Prompt, runID, turn)
			return final, ErrStopped
		}
		if opts.MaxTurns > 0 && turn >= opts.MaxTurns {
			if opts.CanContinue == nil || !opts.CanContinue() {
				if ctx.Err() != nil {
					stopped = true
					final.Answer = stopContent
					final.Reasoning = stopReasoning
					e.finishStopped(tr, final, req.Prompt, runID, turn)
					return final, ErrStopped
				}
				e.finishMaxTurns(tr, final, req.Prompt, opts.MaxTurns)
				return final, ErrMaxTurns
			}
			turn = 0 // a granted continuation resets the turn budget
			if ctx.Err() != nil {
				stopped = true
				final.Answer = stopContent
				final.Reasoning = stopReasoning
				e.finishStopped(tr, final, req.Prompt, runID, turn)
				return final, ErrStopped
			}
		}

		s, err := e.provider.Stream(ctx, provider.Request{
			Model:                 req.Model,
			Messages:              messages,
			StaticPrefixLen:       staticPrefixLen,
			Tools:                 opts.Tools,
			ToolChoice:            opts.ToolChoice,
			ToolSchemaEnforcement: enforceSchema,
			SetCacheKey:           req.SessionKey != "",
			SessionKey:            req.SessionKey,
			ThinkingEnabled:       req.ThinkingEnabled,
			ReasoningEffort:       req.ReasoningEffort,
			ProviderID:            req.ProviderID,
		})
		if err != nil {
			if e.stopped(ctx) {
				stopped = true
				final.Answer = stopContent
				final.Reasoning = stopReasoning
				e.finishStopped(tr, final, req.Prompt, runID, turn)
				return final, ErrStopped
			}
			if provider.IsContextOverflow(err) {
				if opts.Compaction == nil {
					err = errors.New("Provider rejected the request because the context is too large. Context overflow recovery is disabled; enable it or start a new session.")
				} else if recoveredContextOverflow {
					err = errors.New("Provider rejected the request because the context is too large. Eitri summarized older history and retried once, but the request is still too large. Start a new session or reduce attached/tool output.")
				} else if next, ok := e.maybeCompact(ctx, req, opts, messages, true, turn); ok {
					recoveredContextOverflow = true
					messages = next
					continue
				}
			}
			e.emit(TurnEvent{RunID: runID, Turn: turn, EndReason: err.Error()})
			return final, err
		}

		e.emit(TurnEvent{RunID: runID, Turn: turn, Start: true})
		var done provider.Chunk
		var chunks int
		terminal := false
		for {
			c, err := s.Next()
			chunks++
			if errors.Is(err, io.EOF) {
				if e.stopped(ctx) {
					stopped = true
					stopContent += content.String()
					stopReasoning += reasoning.String()
					final.Answer = stopContent
					final.Reasoning = stopReasoning
					e.finishStopped(tr, final, req.Prompt, runID, turn)
					return final, ErrStopped
				}
				if !terminal {
					return final, fmt.Errorf("provider stream truncated after %d chunk(s): %w", chunks, ErrStreamEOF)
				}
				break
			}
			if err != nil {
				if e.stopped(ctx) {
					stopped = true
					stopContent += content.String()
					stopReasoning += reasoning.String()
					final.Answer = stopContent
					final.Reasoning = stopReasoning
					e.finishStopped(tr, final, req.Prompt, runID, turn)
					return final, ErrStopped
				}
				return final, err
			}
			if c.Content != "" {
				content.WriteString(c.Content)
				e.emit(StreamEvent{RunID: runID, Turn: turn, Kind: AnswerStream, Delta: c.Content})
			}
			if c.ReasoningContent != "" {
				reasoning.WriteString(c.ReasoningContent)
				e.emit(StreamEvent{RunID: runID, Turn: turn, Kind: ReasoningStream, Delta: c.ReasoningContent})
			}
			if c.Usage != nil {
				turnUsage = c.Usage
				opts.lastUsage = c.Usage
			}
			done = c
			terminal = terminal || c.Done || c.FinishReason != "" || len(c.ToolCalls) != 0
			if c.Done {
				break
			}
		}
		// Report the turn's own token usage once per turn (the final/last usage chunk),
		// matching the message-layer transcript's last-wins record. Streaming gateways attach
		// a cumulative usage object to every SSE chunk, so summing a UsageEvent per chunk
		// would heavily over-count: telemetry must see one event per turn, not one per chunk.
		// The event carries the turn alone — the live cache/context meter must not become a
		// running sum of the run.
		if turnUsage != nil {
			e.emit(UsageEvent{RunID: runID, Turn: turn, Usage: *turnUsage})
			runUsage = addUsage(runUsage, turnUsage)
		}
		e.emit(TurnEvent{RunID: runID, Turn: turn, EndReason: done.FinishReason})

		assistant := provider.Message{
			Role:              provider.RoleAssistant,
			Content:           content.String(),
			ReasoningContent:  reasoning.String(),
			ThinkingSignature: done.ThinkingSignature,
		}

		if len(done.ToolCalls) == 0 {
			final.Answer = content.String()
			final.Reasoning = reasoning.String()
			// Persist the assistant turn only when it actually said or reasoned something.
			// An empty assistant message (e.g. a stream that ended mid-flight) carries no
			// information and, once sent back to the provider, becomes an invalid
			// `content: null` assistant block that fails the next request.
			if content.Len() > 0 || reasoning.Len() > 0 {
				messages = append(messages, assistant)
			}
			e.storeSessionHistory(req.SessionKey, messages)
			tr.write(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, content.String()))
			return final, nil
		}

		assistant.ToolCalls = done.ToolCalls
		messages = append(messages, assistant)
		dispatchStopped, ranCalls := e.dispatchToolCalls(ctx, opts, runID, turn, done.ToolCalls, &messages)
		totalToolCalls += ranCalls
		if dispatchStopped {
			stopped = true
			stopContent += content.String()
			stopReasoning += reasoning.String()
			final.Answer = stopContent
			final.Reasoning = stopReasoning
			e.finishStopped(tr, final, req.Prompt, runID, turn)
			return final, ErrStopped
		}
		stopContent += content.String()
		stopReasoning += reasoning.String()
	}
}

// maxConcurrentToolCalls bounds how many of a turn's tool calls run at once.
// Each call can spawn a sandbox process, so a model that emits a large batch
// must not fan out that many processes simultaneously; eight keeps a turn's
// wall-clock near its slowest call without oversubscribing the sandbox.
const maxConcurrentToolCalls = 8

// dispatchToolCalls runs a turn's tool calls concurrently, bounded by
// maxConcurrentToolCalls, then drains them on the calling goroutine so tool
// events and provider messages are appended strictly in the model's call order:
// the following provider request is byte-identical to the serial version's. It
// reports whether a stop cut the turn short and how many calls the executor
// actually ran; already-dispatched calls are joined before it returns, so no
// tool goroutine outlives the run.
func (e *Engine) dispatchToolCalls(ctx context.Context, opts AgentOptions, runID, turn int, calls []provider.ToolCall, messages *[]provider.Message) (stopped bool, ran int) {
	type outcome struct {
		result ToolExecResult
		ran    bool
	}
	// A nil slot means the call's goroutine never reached execution (the run
	// stopped while it waited for a slot); a non-nil slot carries that call's
	// result and whether the executor ran it.
	outcomes := make([]*outcome, len(calls))
	done := make([]chan struct{}, len(calls))
	for i := range done {
		done[i] = make(chan struct{})
	}
	// The active set is derived from the conversation at dispatch (the assistant
	// tool-call message is already appended; this turn's results are not) and
	// installed even when empty, so the skill tool can claim a name once per turn:
	// a repeat activation short-circuits and two same-turn calls for one skill
	// cannot both deliver the body. Compaction dropping a body re-activates it.
	ctx = skillindex.WithActiveSkills(ctx, activeSkillNames(*messages))
	sem := make(chan struct{}, maxConcurrentToolCalls)
	var wg sync.WaitGroup
	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc provider.ToolCall) {
			defer wg.Done()
			defer close(done[i])
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			result, executed := execToolCall(ctx, opts, tc)
			outcomes[i] = &outcome{result: result, ran: executed}
		}(i, tc)
	}
	for i, tc := range calls {
		if ctx.Err() != nil {
			stopped = true
			break
		}
		e.emit(ToolCallEvent{RunID: runID, Turn: turn, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		<-done[i]
		if outcomes[i] == nil {
			stopped = true
			break
		}
		result := outcomes[i].result
		var delivered string
		var dropped int
		if result.Uncapped {
			// A skill activation payload is delivered whole: capping it would
			// tail-truncate the body and its closing tag, so the activation scan
			// below would call an unreachable skill active. The tool signals the
			// exemption (rather than the engine matching on payload content) so a
			// result that merely mentions the marker stays capped.
			delivered = result.Text
		} else {
			delivered, dropped = compress.CapBytes(result.Text, compress.DefaultByteCap, result.Dropped, result.BytesDropped)
		}
		e.emit(newToolResultEvent(runID, turn, tc.ID, tc.Name, result, dropped))
		*messages = append(*messages, provider.Message{
			Role:       provider.RoleTool,
			ToolCallID: tc.ID,
			Content:    delivered,
		})
	}
	wg.Wait()
	for _, o := range outcomes {
		if o != nil && o.ran {
			ran++
		}
	}
	return stopped, ran
}

func declaredTool(tools []provider.Tool, name string) (provider.Tool, bool) {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return tool, true
		}
	}
	return provider.Tool{}, false
}

// execToolCall runs one tool call through the hardened dispatch path: it parses and validates the arguments against the tool's strict schema, then executes only when valid. The bool reports whether the executor was invoked; a call rejected by validation returns false.
func execToolCall(ctx context.Context, opts AgentOptions, tc provider.ToolCall) (ToolExecResult, bool) {
	tool, ok := declaredTool(opts.Tools, tc.Name)
	if !ok {
		return ToolExecResult{Text: fmt.Sprintf("error executing tool: undeclared tool %q", tc.Name)}, false
	}
	var parsed map[string]any
	if err := validateToolCallArgs(tool.Function.Parameters, tc.Arguments, &parsed); err != nil {
		if errors.Is(err, errInvalidJSON) {
			b, jerr := json.Marshal(map[string]string{"INVALID_JSON": tc.Arguments})
			if jerr != nil {
				return ToolExecResult{Text: `{"INVALID_JSON":"unserializable"}`}, false
			}
			return ToolExecResult{Text: string(b)}, false
		}
		return ToolExecResult{Text: "invalid tool arguments: " + err.Error()}, false
	}
	result, err := opts.Executor.Execute(ctx, tc.Name, tc.Arguments)
	if err != nil {
		// The executor may still have produced output worth surfacing (bash
		// returns combined stdout+stderr even on a non-zero exit, e.g. an ls
		// that failed for one entry after listing others). Keep the error line
		// first so the TUI's failure tagging still matches, then append any
		// partial output so the model sees what did run.
		msg := "error executing tool: " + err.Error()
		if result.Text != "" {
			msg += "\n" + result.Text
		}
		return ToolExecResult{Text: msg, Compressed: result.Compressed, Dropped: result.Dropped, BytesDropped: result.BytesDropped}, true
	}
	return result, true
}

func (e *Engine) sessionHistory(sessionKey string) []provider.Message {
	if sessionKey == "" {
		return nil
	}
	e.histMu.Lock()
	defer e.histMu.Unlock()
	return append([]provider.Message(nil), e.histories[sessionKey]...)
}

func (e *Engine) storeSessionHistory(sessionKey string, messages []provider.Message) {
	if sessionKey == "" {
		return
	}
	persisted := partitionMessages(messages).PersistedHistory()
	e.histMu.Lock()
	defer e.histMu.Unlock()
	e.histories[sessionKey] = persisted
}

// ClearSessionHistory drops the stored history for sessionKey, freeing the
// memory the engine accumulated for that session. It is called when the
// caller knows the session is being abandoned (e.g. a `/new` re-mint) so
// repeated fresh sessions do not leak prior context indefinitely.
func (e *Engine) ClearSessionHistory(sessionKey string) {
	if sessionKey == "" {
		return
	}
	e.histMu.Lock()
	defer e.histMu.Unlock()
	delete(e.histories, sessionKey)
}
