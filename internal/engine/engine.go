// Package engine drives a single agent run turn over the provider seam. It is
// the shared engine behind both the TUI and batch mode; every code path that
// talks to a model goes through here. This ticket implements the
// non-tool turn: send model + messages, stream deltas, produce the final
// assistant answer, and record the run in the transcript.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/glemsom/eitri/internal/compress"
	"github.com/glemsom/eitri/internal/provider"
)

// ErrMaxTurns is returned when a tool-call loop exceeds the configured cap.
// It bounds runaway agent loops.
var ErrMaxTurns = errors.New("maximum turn limit reached")

// ErrStopped is the dedicated stop sentinel: it wraps context.Canceled so a
// caller distinguishes a user-stopped turn (esc in the TUI) from a failure
// with errors.Is(err, context.Canceled), while the wrapped cause keeps the
// sentinel from matching unrelated errors.
var ErrStopped = fmt.Errorf("turn stopped: %w", context.Canceled)

// TranscriptWriter records the run's on-disk trail.
type TranscriptWriter interface {
	WriteTranscript(line []byte) error
}

// Engine is a run engine bound to a provider and a transcript sink. A caller
// may subscribe to the engine's live event stream via SetListener; the engine
// pushes one typed Event per streamed observation. Unsubscribed
// (batch/headless) runs push nothing and are byte-identical to before.
type Engine struct {
	provider   provider.Provider
	transcript TranscriptWriter
	listener   Listener
}

// New returns an Engine that talks to p and appends run records to tr.
func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr}
}

// Listener receives one typed Event per streamed observation from a live run,
// in order, synchronously from within the turn's drain loop.
type Listener func(Event)

// SetListener subscribes l to the engine's live event stream. A nil listener
// removes the subscription. The TUI (and tests) subscribe here to render a run
// as it happens; batch/headless runs never subscribe and so emit no events.
func (e *Engine) SetListener(l Listener) {
	e.listener = l
}

// emit pushes one Event to the subscriber, no-op when none is attached.
func (e *Engine) emit(evt Event) {
	if e.listener != nil {
		e.listener(evt)
	}
}

// RunRequest is a single non-tool turn of work.
type RunRequest struct {
	Model  string
	Prompt string

	// SkillInject, when non-nil, is a slash-activated skill payload (the rendered
	// <skill_content>/<skill_resources> body). RunAgent prepends it to
	// the provider request head as a system message so the model acts on the args
	// with the skill instructions in context. Nil keeps the historical
	// [system, user] two-message head (byte-identical, prompt-cache invariant).
	SkillInject *string
	// SessionKey opts the run into deepseek's session-scoped prompt cache:
	// every request carries prompt_cache_key:<SessionKey> and the request head
	// stays byte-identical across turns. Empty disables it.
	SessionKey string

	// ThinkingEnabled opts the run into deepseek thinking mode (default on).
	// ReasoningEffort requests the chain-of-thought effort level, normalized on
	// the wire.
	ThinkingEnabled bool
	ReasoningEffort string
}

// Result is the outcome of one Run.
type Result struct {
	Answer    string
	Reasoning string
	Usage     *provider.Usage
}

// systemPromptHead returns the byte-stable embedded Eitri system prompt as the
// immutable request-head message. Every run path —
// agent, special-turn, and headless batch — opens its message list with this
// same message at [0], so the request head (system + tools + verbatim prior
// turns) stays byte-identical across a session: the prompt-cache invariant the
// economics hinge on.
func systemPromptHead() []provider.Message {
	return []provider.Message{{Role: provider.RoleSystem, Content: SystemPromptContent()}}
}

// Run performs a non-tool turn: it sends the model + a user message and streams
// the provider response to a final assistant answer. Thinking is surfaced on a
// separate channel (never merged into the answer) and the run is recorded on
// the transcript sink.
func (e *Engine) Run(ctx context.Context, req RunRequest) (Result, error) {
	// A canceled context refuses to open a provider stream and surfaces the
	// stop sentinel immediately; the stream drain below does the same for a
	// stop that lands mid-flight.
	if ctx.Err() != nil {
		return Result{}, ErrStopped
	}
	s, err := e.provider.Stream(ctx, provider.Request{
		Model:           req.Model,
		Messages:        append(systemPromptHead(), provider.Message{Role: provider.RoleUser, Content: req.Prompt}),
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
	})
	if err != nil {
		return Result{}, err
	}

	return e.drain(ctx, s, req.Prompt, 0)
}

// drain streams s to completion, accumulating the assistant answer, reasoning,
// and terminal usage into a Result, writing the run to the transcript sink, and
// returning the finished Result. It is the shared tail of every non-tool turn
// (Run, RunJSONObjectMode, RunSamplingPolicy) so the stream-drain loop and the
// transcript write are authored once instead of copied per turn type.
func (e *Engine) drain(ctx context.Context, s provider.Stream, prompt string, turn int) (Result, error) {
	e.emit(TurnEvent{Turn: turn, Start: true})
	var res Result
	var endReason string
	for {
		c, err := s.Next()
		if err != nil {
			ce := closeErr(err)
			if ce == nil {
				break
			}
			// A provider stream that dies because its wire context was canceled
			// is a user stop, not a failure: preserve the partial content and
			// write the stopped record instead of surfacing the raw transport
			// error.
			if e.stopped(ctx) {
				e.finishStopped(res, prompt, turn)
				return res, ErrStopped
			}
			return res, ce
		}
		if c.Content != "" {
			res.Answer += c.Content
			e.emit(StreamEvent{Turn: turn, Kind: AnswerStream, Delta: c.Content})
		}
		if c.ReasoningContent != "" {
			res.Reasoning += c.ReasoningContent
			e.emit(StreamEvent{Turn: turn, Kind: ReasoningStream, Delta: c.ReasoningContent})
		}
		if c.Usage != nil {
			res.Usage = c.Usage
			e.emit(UsageEvent{Turn: turn, Usage: *c.Usage})
		}
		endReason = c.FinishReason
		if c.Done {
			break
		}
	}
	e.emit(TurnEvent{Turn: turn, EndReason: endReason})

	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", prompt, res.Answer))
	}
	return res, nil
}

// stopped reports whether the caller's context was canceled, the condition that
// turns a stream/tool error into a user stop rather than a failure.
func (e *Engine) stopped(ctx context.Context) bool {
	return ctx.Err() != nil
}

// finishStopped emits the turn-ending event and the stopped transcript record
// for a run aborted by cancellation. The record carries the same header shape
// as a clean run plus the partial content accumulated so far, with the stopped
// marker so the session trail distinguishes an aborted run from a clean one; a
// clean run's record bytes are unchanged.
func (e *Engine) finishStopped(res Result, prompt string, turn int) {
	e.emit(TurnEvent{Turn: turn, EndReason: "stopped"})
	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n[stopped]\n", prompt, res.Answer))
	}
}

// jsonObjectSuffix is appended to a JSON Object Mode prompt when the caller's
// text does not already ask for JSON. The provider's json_object response_format
// contract requires the prompt to contain the word "json" in some form (the
// DeepSeek/OpenCode gateway hard-rejects a prompt that omits it with HTTP 400,
// "Prompt must contain the word 'json' in some form"). Appending a fixed
// sentence guarantees the contract is met without mutating caller-owned input.
const jsonObjectSuffix = "\n\nPlease output a JSON object."

// jsonObjectPrompt returns prompt, appending jsonObjectSuffix only when the
// prompt does not already ask for JSON (case-insensitive). A prompt that already
// mentions JSON is left byte-identical, so a caller that explicitly requests a
// JSON shape gets exactly what it wrote.
func jsonObjectPrompt(prompt string) string {
	if strings.Contains(strings.ToLower(prompt), "json") {
		return prompt
	}
	return prompt + jsonObjectSuffix
}

// RunJSONObjectMode runs a JSON Object Mode finalization turn: an internal, non-tool special turn that requires
// provider-side JSON Object Mode so the final answer is a valid JSON object
// without mixing structured-output rules into an ordinary agent/tool loop. It
// pre-flights the json_object_mode control as required — an unsupported
// provider fails negotiation fast, before any wire call, via
// provider.UnsupportedRequiredControlError — then streams a non-tool turn
// flagged for JSON Object Mode and returns the finalized JSON answer.
func (e *Engine) RunJSONObjectMode(ctx context.Context, req RunRequest) (Result, error) {
	if _, err := e.NegotiateGenerationControls(ctx, []provider.ControlRequirement{
		{Control: provider.GenerationControlJSONObjectMode, Required: true},
	}); err != nil {
		return Result{}, err
	}

	s, err := e.provider.Stream(ctx, provider.Request{
		Model:           req.Model,
		Messages:        append(systemPromptHead(), provider.Message{Role: provider.RoleUser, Content: jsonObjectPrompt(req.Prompt)}),
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
		JSONObjectMode:  true,
	})
	if err != nil {
		return Result{}, err
	}

	return e.drain(ctx, s, req.Prompt, 0)
}

// RunSamplingPolicy runs a Sampling Policy special turn: an internal, non-tool turn that requests temperature- or
// nucleus-based sampling for a constrained generation. It pre-flights the
// sampling_policy control as required — an unsupported provider fails
// negotiation fast, before any wire call, via
// provider.UnsupportedRequiredControlError — then streams a non-tool turn
// carrying exactly the requested policy (so the wire emits temperature or top_p,
// never both) and returns the generated answer. Ordinary turns never carry a
// sampling policy and stay on provider defaults.
func (e *Engine) RunSamplingPolicy(ctx context.Context, req RunRequest, policy provider.SamplingPolicy) (Result, error) {
	if _, err := e.NegotiateGenerationControls(ctx, []provider.ControlRequirement{
		{Control: provider.GenerationControlSamplingPolicy, Required: true},
	}); err != nil {
		return Result{}, err
	}

	s, err := e.provider.Stream(ctx, provider.Request{
		Model:           req.Model,
		Messages:        append(systemPromptHead(), provider.Message{Role: provider.RoleUser, Content: req.Prompt}),
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
		Sampling:        &policy,
	})
	if err != nil {
		return Result{}, err
	}

	return e.drain(ctx, s, req.Prompt, 0)
}

// ToolExecutor executes an agent tool call. The tools registry implements it;
// the engine depends on this seam so dispatch is testable without filesystem
// side effects a specific tool might have. The returned ToolExecResult carries
// the result text plus whether it is the line-compressor's compressed form:
// the engine relies on this truth — never on matching a
// look-alike "+N more" tail — to decide whether the byte-cap may merge that
// tail as the compressor's marker.
type ToolExecResult struct {
	// Text is the tool's result string.
	Text string
	// Compressed is true when Text is the line-compressor's compressed form.
	Compressed bool
}

type ToolExecutor interface {
	Execute(ctx context.Context, name string, argsJSON string) (ToolExecResult, error)
}

// ExecutorFunc adapts a plain function to the ToolExecutor interface.
type ExecutorFunc func(ctx context.Context, name string, argsJSON string) (ToolExecResult, error)

// Execute implements ToolExecutor.
func (f ExecutorFunc) Execute(ctx context.Context, name, argsJSON string) (ToolExecResult, error) {
	return f(ctx, name, argsJSON)
}

// AgentOptions configures the tool-call dispatch loop.
// Tools and ToolChoice are the request-head tool definitions sent to the
// provider (kept stable per session for prompt caching); Executor runs calls;
// MaxTurns caps the loop (0 = uncapped). CanContinue, when set, lets an
// interactive caller grant another budget once the cap is hit instead of the
// loop failing.
type AgentOptions struct {
	Tools      []provider.Tool
	ToolChoice any
	// SchemaEnforcement opts a tool-capable agent loop into provider-side Tool
	// Schema Enforcement: when the provider honors the
	// tool_schema_enforcement control, every turn's tool manifest carries
	// strict:true so the provider rejects schema-violating tool arguments at
	// generation time. Local tool-argument validation remains the mandatory
	// safety floor before execution regardless. A provider that cannot honor it
	// degrades deterministically (strict is simply omitted) — never a failure,
	// since local validation already guards execution.
	ToolSchemaEnforcement bool
	Executor              ToolExecutor
	MaxTurns              int

	// CanContinue is asked when MaxTurns (>0) is reached and the loop wants to
	// keep going. When nil — the batch/headless default — the loop stops with
	// ErrMaxTurns (auto-deny changes). When it returns true, the loop is granted
	// a fresh MaxTurns budget and continues; a false return stops with ErrMaxTurns.
	// It is the interactive "pause at the cap and prompt to continue" boundary.
	CanContinue func() bool

	// Compaction, when non-nil, enables the unified session compaction engine
	// After each turn it compacts when prompt usage crosses the
	// configured fraction of the context window, and emergently on a provider
	// context-overflow: the oldest body is evicted, the verbatim tail (last
	// TailTurns pairs, reasoning included) is kept, and an anchored summary is
	// re-injected at the head. lastUsage is the running usage so the compaction
	// check sees the most recent turn's utilization.
	Compaction  *CompactionConfig
	OnCompacted func()

	// lastUsage records the most recent turn's token usage, read by the
	// compaction engine (never set by callers).
	lastUsage *provider.Usage
}

// NegotiateGenerationControls pre-flights a special turn's generation-control
// requirements against this engine's provider capability surface.
// It forwards to the provider seam; the returned controls are
// the ones the provider will honor — required controls the provider cannot honor
// fail here, before any wire call, while unsupported optional controls are
// dropped. It is the seam generation-control-aware special turns consult before streaming.
func (e *Engine) NegotiateGenerationControls(ctx context.Context, reqs []provider.ControlRequirement) ([]provider.GenerationControl, error) {
	return provider.NegotiateGenerationControls(ctx, e.provider, reqs)
}

// RunAgent drives a tool-capable agent run: it maintains one mutable messages
// list, executes any returned tool_calls (single-call path is the floor here;
// hardening is T5), appends a matching role:"tool" result per call, and
// resubmits until the model stops calling tools. Result.Answer/Reasoning/Usage
// reflect the final, tool-free turn.
func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (Result, error) {
	// A stop landing before any turn runs refuses to wire fresh provider work
	// and surfaces the stop sentinel immediately.
	if ctx.Err() != nil {
		return Result{}, ErrStopped
	}
	// Build the message head once from a conditional skill prefix:
	// the system prompt sits at [0], the slash-activated skill payload (when
	// present) follows as a second RoleSystem message, then the user args. The
	// no-inject path stays byte-identical to the historical [system, user] head.
	messages := systemPromptHead()
	if req.SkillInject != nil {
		messages = append(messages,
			provider.Message{Role: provider.RoleSystem, Content: *req.SkillInject})
	}
	messages = append(messages, provider.Message{Role: provider.RoleUser, Content: req.Prompt})
	var (
		final Result
		// stopContent/stopReasoning accumulate the partial output of every turn
		// streamed before a stop, so a canceled tool-loop run keeps the assistant
		// text it had already produced (the final answer surfaces only the last
		// turn's content).
		stopContent   string
		stopReasoning string
	)

	// Optionally opt this agent loop into provider-side Tool Schema Enforcement:
	// pre-flight the control as an optional requirement so an
	// unsupported provider degrades deterministically (strict is dropped) without
	// blocking the loop — local validation remains the mandatory safety floor.
	// This pre-flight is done once, before any wire call.
	enforceSchema := false
	if opts.ToolSchemaEnforcement {
		honored, err := e.NegotiateGenerationControls(ctx, []provider.ControlRequirement{
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

	for turn := 0; ; turn++ {
		var content, reasoning string
		// The cancellation boundary between turns: once the caller's context is
		// canceled the loop must not open another provider stream or run another
		// tool; the output accumulated so far becomes the stopped result.
		if ctx.Err() != nil {
			final.Answer = stopContent
			final.Reasoning = stopReasoning
			e.finishStopped(final, req.Prompt, turn)
			return final, ErrStopped
		}
		if opts.MaxTurns > 0 && turn >= opts.MaxTurns {
			// The cap is reached. Batch/headless (no hook) auto-denies: stop.
			// Interactive callers may grant another budget via CanContinue.
			if opts.CanContinue == nil || !opts.CanContinue() {
				return final, ErrMaxTurns
			}
			turn = 0 // a granted continuation resets the turn budget
		}

		// Proactive compaction check from the prior turn's usage: if the session
		// crossed the threshold, evict the oldest body and rebuild the summary
		// head before streaming the next request.
		if opts.Compaction != nil {
			messages, _ = e.maybeCompact(ctx, req, opts, messages, false, turn)
		}

		s, err := e.provider.Stream(ctx, provider.Request{
			Model:                 req.Model,
			Messages:              messages,
			Tools:                 opts.Tools,
			ToolChoice:            opts.ToolChoice,
			ToolSchemaEnforcement: enforceSchema,
			SetCacheKey:           req.SessionKey != "",
			SessionKey:            req.SessionKey,
			ThinkingEnabled:       req.ThinkingEnabled,
			ReasoningEffort:       req.ReasoningEffort,
		})
		if err != nil {
			// Emergency overflow trigger: a context-overflow below the proactive
			// threshold fires the same compaction engine (evict oldest + rebuild
			// summary head) and retries rather than surfacing the raw overflow.
			// A compaction that no longer reduces the
			// messages falls through and the overflow is returned.
			if opts.Compaction != nil && provider.IsContextOverflow(err) {
				if next, ok := e.maybeCompact(ctx, req, opts, messages, true, turn); ok {
					messages = next
					continue
				}
			}
			e.emit(TurnEvent{Turn: turn, EndReason: err.Error()})
			return final, err
		}

		// Emit the turn boundary only once the provider stream opened: an
		// overflowed-and-retried turn carried no streamed output and emits no
		// Start, so the event stream never pairs a Start without a matching End.
		e.emit(TurnEvent{Turn: turn, Start: true})
		var done provider.Chunk
		for {
			c, err := s.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				// A provider stream that dies because the caller canceled the turn
				// is a stop: keep the partial content, write the stopped record,
				// and refuse to resubmit past the cancellation boundary.
				if e.stopped(ctx) {
					stopContent += content
					stopReasoning += reasoning
					final.Answer = stopContent
					final.Reasoning = stopReasoning
					e.finishStopped(final, req.Prompt, turn)
					return final, ErrStopped
				}
				return final, err
			}
			if c.Content != "" {
				content += c.Content
				e.emit(StreamEvent{Turn: turn, Kind: AnswerStream, Delta: c.Content})
			}
			if c.ReasoningContent != "" {
				reasoning += c.ReasoningContent
				e.emit(StreamEvent{Turn: turn, Kind: ReasoningStream, Delta: c.ReasoningContent})
			}
			if c.Usage != nil {
				final.Usage = c.Usage
				opts.lastUsage = c.Usage
				e.emit(UsageEvent{Turn: turn, Usage: *c.Usage})
			}
			done = c
			if c.Done {
				break
			}
		}
		e.emit(TurnEvent{Turn: turn, EndReason: done.FinishReason})

		assistant := provider.Message{
			Role: provider.RoleAssistant,
			// DeepSeek requires assistant messages to carry reasoning_content
			// (empty-ok) and real reasoning to persist on tool turns.
			Content:          content,
			ReasoningContent: reasoning,
		}

		// No tool calls on the terminal chunk: this turn is the final answer.
		if len(done.ToolCalls) == 0 {
			final.Answer = content
			final.Reasoning = reasoning
			if e.transcript != nil {
				_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, content))
			}
			return final, nil
		}

		assistant.ToolCalls = done.ToolCalls
		messages = append(messages, assistant)
		for _, tc := range done.ToolCalls {
			if e.stopped(ctx) {
				stopContent += content
				stopReasoning += reasoning
				final.Answer = stopContent
				final.Reasoning = stopReasoning
				e.finishStopped(final, req.Prompt, turn)
				return final, ErrStopped
			}
			e.emit(ToolCallEvent{Turn: turn, ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
			result := execToolCall(ctx, opts, tc)
			// Shared byte-cap at the tool-result boundary: every tool
			// result is measured against the single budget before its bytes enter
			// message history, so one oversized web_fetch or whole-file read cannot
			// exhaust the context window. The delivered (byte-capped) form goes to
			// the provider; the event additionally carries the FULL pre-cap result
			// so the TUI expand path stays lossless. CapBytes
			// merges a "+N more" tail into the byte marker ONLY when the executor
			// reports the result is the line-compressor's compressed form — raw
			// content that merely looks like the marker is never stripped.
			delivered, dropped := compress.CapBytes(result.Text, compress.DefaultByteCap, result.Compressed)
			e.emit(newToolResultEvent(turn, tc.ID, tc.Name, result.Text, dropped))
			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    delivered,
			})
		}
		// Accumulate the turn's content into the stop accumulator so a
		// cancellation that lands between this turn and the next preserves
		// all partial output in the transcript record.
		stopContent += content
		stopReasoning += reasoning
	}
}

// toolSchema returns the canonical strict-shaped Parameters map for the named
// tool from the request-head tool manifest, or nil if the tool is unknown/not
// validated. The schema is the canonical form re-expressed per dialect, so it
// is the single validation target.
func toolSchema(tools []provider.Tool, name string) map[string]any {
	for _, t := range tools {
		if t.Function.Name == name {
			return t.Function.Parameters
		}
	}
	return nil
}

// execToolCall runs one tool call through the hardened dispatch path: it
// parses and validates the arguments against the tool's strict schema, then
// executes only when valid. Malformed JSON is routed to a wrapped
// {"INVALID_JSON": "<raw>"} tool result (built via the JSON library so
// escaping stays correct); a schema-violating call is rejected with a
// descriptive error. It never panics and never silently skips a call.
func execToolCall(ctx context.Context, opts AgentOptions, tc provider.ToolCall) ToolExecResult {
	var parsed map[string]any
	if err := validateToolCallArgs(toolSchema(opts.Tools, tc.Name), tc.Arguments, &parsed); err != nil {
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
		return ToolExecResult{Text: "error executing tool: " + err.Error()}
	}
	return result
}

func closeErr(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
