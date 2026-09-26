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

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/provider"
)

// ErrMaxTurns is returned when a tool-call loop exceeds the configured cap.
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

// Engine is a run engine bound to a provider and a transcript sink.
type Engine struct {
	provider   provider.Provider
	transcript TranscriptWriter
	listener   Listener

	histMu    sync.Mutex
	histories map[string][]provider.Message

	runMu   sync.Mutex
	nextRun int
}

// New returns an Engine that talks to p and appends run records to tr.
func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr, histories: make(map[string][]provider.Message)}
}

func (e *Engine) SetTranscript(tr TranscriptWriter) {
	e.transcript = tr
}

// Listener receives one typed Event per streamed observation from a live run, in order, synchronously from within the turn's drain loop.
type Listener func(Event)

// SetListener subscribes l to the engine's live event stream.
func (e *Engine) SetListener(l Listener) {
	e.listener = l
}

// emit pushes one Event to the subscriber, no-op when none is attached.
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

// Result is the outcome of one Run.
type Result struct {
	Answer    string
	Reasoning string
	Usage     *provider.Usage

	// Turns is how many provider request/response pairs the run performed, tool-calling turns included.
	Turns int

	// Stopped reports whether the run ended in a user stop (ErrStopped) rather
	// than a normal completion or a failure.
	Stopped bool
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
	d := "## Working directory\nYou are operating in the workspace `" + workspace + "`. Resolve all relative paths against it."
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

// finishStopped emits the turn-ending event and the stopped transcript record for a run aborted by cancellation.
func (e *Engine) finishStopped(res Result, prompt string, runID, turn int) {
	e.emit(TurnEvent{RunID: runID, Turn: turn, EndReason: "stopped"})
	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n[stopped]\n", prompt, res.Answer))
	}
}

// ToolExecResult is one tool call's outcome as the engine sees it.
type ToolExecResult struct {
	Text       string
	Compressed bool
	Dropped    int

	// BytesDropped is the count of bytes an upstream memory bound (the sandbox
	// buffer) already rejected from the result's stream; the engine's byte cap
	// folds it into the single authoritative truncation marker.
	BytesDropped int
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

func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (final Result, _ error) {
	if len(opts.Tools) > 0 && opts.Executor == nil {
		return Result{}, errors.New("declared tools require a tool executor")
	}
	runID := e.claimRunID()
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
	stopped := false
	defer func() {
		final.Turns = totalTurns
		final.Stopped = stopped
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
		if ctx.Err() != nil {
			stopped = true
			final.Answer = stopContent
			final.Reasoning = stopReasoning
			e.finishStopped(final, req.Prompt, runID, turn)
			return final, ErrStopped
		}
		if opts.MaxTurns > 0 && turn >= opts.MaxTurns {
			if opts.CanContinue == nil || !opts.CanContinue() {
				if ctx.Err() != nil {
					stopped = true
					final.Answer = stopContent
					final.Reasoning = stopReasoning
					e.finishStopped(final, req.Prompt, runID, turn)
					return final, ErrStopped
				}
				return final, ErrMaxTurns
			}
			turn = 0 // a granted continuation resets the turn budget
			if ctx.Err() != nil {
				stopped = true
				final.Answer = stopContent
				final.Reasoning = stopReasoning
				e.finishStopped(final, req.Prompt, runID, turn)
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
				e.finishStopped(final, req.Prompt, runID, turn)
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
					e.finishStopped(final, req.Prompt, runID, turn)
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
					e.finishStopped(final, req.Prompt, runID, turn)
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
				final.Usage = c.Usage
				opts.lastUsage = c.Usage
			}
			done = c
			terminal = terminal || c.Done || c.FinishReason != "" || len(c.ToolCalls) != 0
			if c.Done {
				break
			}
		}
		// Report token usage once per turn (the final/last usage chunk), matching the
		// message-layer transcript's last-wins record. Streaming gateways attach a cumulative
		// usage object to every SSE chunk, so summing a UsageEvent per chunk would heavily
		// over-count: telemetry must see one event per turn, not one per chunk.
		if final.Usage != nil {
			e.emit(UsageEvent{RunID: runID, Turn: turn, Usage: *final.Usage})
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
			if e.transcript != nil {
				_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, content.String()))
			}
			return final, nil
		}

		assistant.ToolCalls = done.ToolCalls
		messages = append(messages, assistant)
		for _, tc := range done.ToolCalls {
			if e.stopped(ctx) {
				stopped = true
				stopContent += content.String()
				stopReasoning += reasoning.String()
				final.Answer = stopContent
				final.Reasoning = stopReasoning
				e.finishStopped(final, req.Prompt, runID, turn)
				return final, ErrStopped
			}
			e.emit(ToolCallEvent{RunID: runID, Turn: turn, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			result := execToolCall(ctx, opts, tc)
			delivered, dropped := compress.CapBytes(result.Text, compress.DefaultByteCap, result.Dropped, result.BytesDropped)
			e.emit(newToolResultEvent(runID, turn, tc.ID, tc.Name, result, dropped))
			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    delivered,
			})
		}
		stopContent += content.String()
		stopReasoning += reasoning.String()
	}
}

func declaredTool(tools []provider.Tool, name string) (provider.Tool, bool) {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return tool, true
		}
	}
	return provider.Tool{}, false
}

// execToolCall runs one tool call through the hardened dispatch path: it parses and validates the arguments against the tool's strict schema, then executes only when valid.
func execToolCall(ctx context.Context, opts AgentOptions, tc provider.ToolCall) ToolExecResult {
	tool, ok := declaredTool(opts.Tools, tc.Name)
	if !ok {
		return ToolExecResult{Text: fmt.Sprintf("error executing tool: undeclared tool %q", tc.Name)}
	}
	var parsed map[string]any
	if err := validateToolCallArgs(tool.Function.Parameters, tc.Arguments, &parsed); err != nil {
		if errors.Is(err, errInvalidJSON) {
			b, jerr := json.Marshal(map[string]string{"INVALID_JSON": tc.Arguments})
			if jerr != nil {
				return ToolExecResult{Text: `{"INVALID_JSON":"unserializable"}`}
			}
			return ToolExecResult{Text: string(b)}
		}
		return ToolExecResult{Text: "invalid tool arguments: " + err.Error()}
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
		return ToolExecResult{Text: msg, Compressed: result.Compressed, Dropped: result.Dropped, BytesDropped: result.BytesDropped}
	}
	return result
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
