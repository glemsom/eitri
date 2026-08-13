// Package engine drives a single agent run turn over the provider seam. It is
// the shared engine behind both the TUI and batch mode; every code path that
// talks to a model goes through here. This ticket (T1c) implements the
// non-tool turn: send model + messages, stream deltas, produce the final
// assistant answer, and record the run in the transcript.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/glemsom/eitri/internal/provider"
)

// ErrMaxTurns is returned when a tool-call loop exceeds the configured cap.
// It bounds runaway agent loops (docs/spec.md §2.1, eitri.md §2.1).
var ErrMaxTurns = errors.New("maximum turn limit reached")

// TranscriptWriter records the run's on-disk trail (the T1b session sink).
type TranscriptWriter interface {
	WriteTranscript(line []byte) error
}

// Engine is a run engine bound to a provider and a transcript sink.
type Engine struct {
	provider   provider.Provider
	transcript TranscriptWriter
}

// New returns an Engine that talks to p and appends run records to tr.
func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr}
}

// RunRequest is a single non-tool turn of work.
type RunRequest struct {
	Model  string
	Prompt string
	// SessionKey opts the run into deepseek's session-scoped prompt cache:
	// every request carries prompt_cache_key:<SessionKey> and the request head
	// stays byte-identical across turns (docs/spec.md §4). Empty disables it.
	SessionKey string

	// ThinkingEnabled opts the run into deepseek thinking mode (default on).
	// ReasoningEffort requests the chain-of-thought effort level, normalized on
	// the wire (docs/spec.md §6).
	ThinkingEnabled bool
	ReasoningEffort string
}

// Result is the outcome of one Run.
type Result struct {
	Answer    string
	Reasoning string
	Usage     *provider.Usage
}

// Run performs a non-tool turn: it sends the model + a user message and streams
// the provider response to a final assistant answer. Thinking is surfaced on a
// separate channel (never merged into the answer) and the run is recorded on
// the transcript sink.
func (e *Engine) Run(ctx context.Context, req RunRequest) (Result, error) {
	s, err := e.provider.Stream(ctx, provider.Request{
		Model:           req.Model,
		Messages:        []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}},
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
	})
	if err != nil {
		return Result{}, err
	}

	var res Result
	for {
		c, err := s.Next()
		if err != nil {
			return res, closeErr(err)
		}
		res.Answer += c.Content
		res.Reasoning += c.ReasoningContent
		if c.Usage != nil {
			res.Usage = c.Usage
		}
		if c.Done {
			break
		}
	}

	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, res.Answer))
	}
	return res, nil
}

// RunJSONObjectMode runs a JSON Object Mode finalization turn (issue #59,
// docs/spec.md §13): an internal, non-tool special turn that requires
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
		Messages:        []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}},
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
		JSONObjectMode:  true,
	})
	if err != nil {
		return Result{}, err
	}

	var res Result
	for {
		c, err := s.Next()
		if err != nil {
			return res, closeErr(err)
		}
		res.Answer += c.Content
		res.Reasoning += c.ReasoningContent
		if c.Usage != nil {
			res.Usage = c.Usage
		}
		if c.Done {
			break
		}
	}
	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, res.Answer))
	}
	return res, nil
}

// RunSamplingPolicy runs a Sampling Policy special turn (issue #61,
// docs/spec.md §13): an internal, non-tool turn that requests temperature- or
// nucleus-based sampling for a constrained generation. It pre-flights the
// sampling_policy control as required — an unsupported provider fails
// negotiation fast, before any wire call, via provider.UnsupportedRequiredControlError —
// then streams a non-tool turn carrying exactly the requested policy (so the
// wire emits temperature or top_p, never both) and returns the generated answer.
// Ordinary agent/tool turns never carry a sampling policy and stay on provider
// defaults.
func (e *Engine) RunSamplingPolicy(ctx context.Context, req RunRequest, policy provider.SamplingPolicy) (Result, error) {
	if _, err := e.NegotiateGenerationControls(ctx, []provider.ControlRequirement{
		{Control: provider.GenerationControlSamplingPolicy, Required: true},
	}); err != nil {
		return Result{}, err
	}

	s, err := e.provider.Stream(ctx, provider.Request{
		Model:           req.Model,
		Messages:        []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}},
		SetCacheKey:     req.SessionKey != "",
		SessionKey:      req.SessionKey,
		ThinkingEnabled: req.ThinkingEnabled,
		ReasoningEffort: req.ReasoningEffort,
		Sampling:        &policy,
	})
	if err != nil {
		return Result{}, err
	}

	var res Result
	for {
		c, err := s.Next()
		if err != nil {
			return res, closeErr(err)
		}
		res.Answer += c.Content
		res.Reasoning += c.ReasoningContent
		if c.Usage != nil {
			res.Usage = c.Usage
		}
		if c.Done {
			break
		}
	}
	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n", req.Prompt, res.Answer))
	}
	return res, nil
}

// ToolExecutor executes an agent tool call. The tools registry implements it;
// the engine depends on this seam so dispatch is testable without filesystem
// side effects a specific tool might have.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, argsJSON string) (string, error)
}

// ExecutorFunc adapts a plain function to the ToolExecutor interface.
type ExecutorFunc func(ctx context.Context, name string, argsJSON string) (string, error)

// Execute implements ToolExecutor.
func (f ExecutorFunc) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	return f(ctx, name, argsJSON)
}

// AgentOptions configures the tool-call dispatch loop (docs/spec.md §2).
// Tools and ToolChoice are the request-head tool definitions sent to the
// provider (kept stable per session for prompt caching); Executor runs calls;
// MaxTurns caps the loop (0 = uncapped). CanContinue, when set, lets an
// interactive caller grant another budget once the cap is hit instead of the
// loop failing (eitri.md §2.1).
type AgentOptions struct {
	Tools      []provider.Tool
	ToolChoice any
	// SchemaEnforcement opts a tool-capable agent loop into provider-side Tool
	// Schema Enforcement (issue #62): when the provider honors the
	// tool_schema_enforcement control, every turn's tool manifest carries
	// strict:true so the provider rejects schema-violating tool arguments at
	// generation time. Local tool-argument validation remains the mandatory
	// safety floor before execution regardless. A provider that cannot honor it
	// degrades deterministically (strict is simply omitted) — never a failure,
	// since local validation already guards execution (docs/spec.md §13).
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
	// (ADR-0003, T10). After each turn it compacts when prompt usage crosses the
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
// requirements against this engine's provider capability surface (docs/spec.md
// §13 / issue #58). It forwards to the provider seam; the returned controls are
// the ones the provider will honor — required controls the provider cannot honor
// fail here, before any wire call, while unsupported optional controls are
// dropped. It is the seam generation-control-aware special turns (issues #59–#62)
// consult before streaming.
func (e *Engine) NegotiateGenerationControls(ctx context.Context, reqs []provider.ControlRequirement) ([]provider.GenerationControl, error) {
	return provider.NegotiateGenerationControls(ctx, e.provider, reqs)
}

// RunAgent drives a tool-capable agent run: it maintains one mutable messages
// list, executes any returned tool_calls (single-call path is the floor here;
// hardening is T5), appends a matching role:"tool" result per call, and
// resubmits until the model stops calling tools. Result.Answer/Reasoning/Usage
// reflect the final, tool-free turn.
func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (Result, error) {
	messages := []provider.Message{{Role: provider.RoleUser, Content: req.Prompt}}
	var final Result

	// Optionally opt this agent loop into provider-side Tool Schema Enforcement
	// (issue #62): pre-flight the control as an optional requirement so an
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
			messages, _ = e.maybeCompact(ctx, req, opts, messages, false)
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
			// summary head) and retries rather than surfacing the raw overflow
			// (ADR-0003 decision 2). A compaction that no longer reduces the
			// messages falls through and the overflow is returned.
			if opts.Compaction != nil && provider.IsContextOverflow(err) {
				if next, ok := e.maybeCompact(ctx, req, opts, messages, true); ok {
					messages = next
					continue
				}
			}
			return final, err
		}

		var content, reasoning string
		var done provider.Chunk
		for {
			c, err := s.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return final, err
			}
			content += c.Content
			reasoning += c.ReasoningContent
			if c.Usage != nil {
				final.Usage = c.Usage
				opts.lastUsage = c.Usage
			}
			done = c
			if c.Done {
				break
			}
		}

		assistant := provider.Message{
			Role: provider.RoleAssistant,
			// DeepSeek requires assistant messages to carry reasoning_content
			// (empty-ok) and real reasoning to persist on tool turns (spec §6).
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
			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    execToolCall(ctx, opts, tc),
			})
		}
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
func execToolCall(ctx context.Context, opts AgentOptions, tc provider.ToolCall) string {
	var parsed map[string]any
	if err := validateToolCallArgs(toolSchema(opts.Tools, tc.Name), tc.Arguments, &parsed); err != nil {
		if errors.Is(err, errInvalidJSON) {
			b, jerr := json.Marshal(map[string]string{"INVALID_JSON": tc.Arguments})
			if jerr != nil {
				return `{"INVALID_JSON":"unserializable"}`
			}
			return string(b)
		}
		return "invalid tool arguments: " + err.Error()
	}
	result, err := opts.Executor.Execute(ctx, tc.Name, tc.Arguments)
	if err != nil {
		return "error executing tool: " + err.Error()
	}
	return result
}

func closeErr(err error) error {
	if err == io.EOF {
		return nil
	}
	return err
}
