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
}

// New returns an Engine that talks to p and appends run records to tr.
func New(p provider.Provider, tr TranscriptWriter) *Engine {
	return &Engine{provider: p, transcript: tr, histories: make(map[string][]provider.Message)}
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

// RunRequest is a single non-tool turn of work.
type RunRequest struct {
	Model  string
	Prompt string

	SkillInject *string
	SessionKey  string

	ThinkingEnabled bool
	ReasoningEffort string
}

// Result is the outcome of one Run.
type Result struct {
	Answer    string
	Reasoning string
	Usage     *provider.Usage
}

// systemPromptHead returns the byte-stable embedded Eitri system prompt as the immutable request-head message.
func systemPromptHead() []provider.Message {
	return []provider.Message{{Role: provider.RoleSystem, Content: SystemPromptContent()}}
}

// bindSkillToPrompt folds a slash-injected skill payload into the single
// high-priority user message that carries the prompt. Delivering the directive
// inside the user layer (rather than as a competing second system message) puts
// it adjacent to the prompt so it outranks the Eitri persona when the two
// conflict; smaller models deprioritize instructions they receive in a system
// message, which is why the old second-system-message injection lost.
func bindSkillToPrompt(prompt, skill string) string {
	var b strings.Builder
	b.WriteString("The user invoked this skill by name; follow its instructions exactly. They are binding, not advisory, and a conflicting system persona does not override them.\n\n")
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
func (e *Engine) finishStopped(res Result, prompt string, turn int) {
	e.emit(TurnEvent{Turn: turn, EndReason: "stopped"})
	if e.transcript != nil {
		_ = e.transcript.WriteTranscript(fmt.Appendf(nil, "=== %s ===\n%s\n[stopped]\n", prompt, res.Answer))
	}
}

// ToolExecutor executes an agent tool call.
type ToolExecResult struct {
	Text       string
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

// NegotiateGenerationControls pre-flights a special turn's generation-control requirements against this engine's provider capability surface.
func (e *Engine) NegotiateGenerationControls(ctx context.Context, reqs []provider.ControlRequirement) ([]provider.GenerationControl, error) {
	return provider.NegotiateGenerationControls(ctx, e.provider, reqs)
}

// RunAgent drives a tool-capable agent run: it maintains one mutable messages list, executes any returned tool_calls (single-call path is the floor here; hardening is T5), appends a matching role:"tool" result per call, and resubmits until the model stops calling tools.
func (e *Engine) RunAgent(ctx context.Context, req RunRequest, opts AgentOptions) (Result, error) {
	if ctx.Err() != nil {
		return Result{}, ErrStopped
	}
	messages := systemPromptHead()
	messages = append(messages, e.sessionHistory(req.SessionKey)...)
	userContent := req.Prompt
	if req.SkillInject != nil {
		userContent = bindSkillToPrompt(userContent, *req.SkillInject)
	}
	messages = append(messages, provider.Message{Role: provider.RoleUser, Content: userContent})
	var (
		final         Result
		stopContent   string
		stopReasoning string
	)

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
		if ctx.Err() != nil {
			final.Answer = stopContent
			final.Reasoning = stopReasoning
			e.finishStopped(final, req.Prompt, turn)
			return final, ErrStopped
		}
		if opts.MaxTurns > 0 && turn >= opts.MaxTurns {
			if opts.CanContinue == nil || !opts.CanContinue() {
				return final, ErrMaxTurns
			}
			turn = 0 // a granted continuation resets the turn budget
		}

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
			if opts.Compaction != nil && provider.IsContextOverflow(err) {
				if next, ok := e.maybeCompact(ctx, req, opts, messages, true, turn); ok {
					messages = next
					continue
				}
			}
			e.emit(TurnEvent{Turn: turn, EndReason: err.Error()})
			return final, err
		}

		e.emit(TurnEvent{Turn: turn, Start: true})
		var done provider.Chunk
		for {
			c, err := s.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
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
			Role:             provider.RoleAssistant,
			Content:          content,
			ReasoningContent: reasoning,
		}

		if len(done.ToolCalls) == 0 {
			messages = append(messages, assistant)
			final.Answer = content
			final.Reasoning = reasoning
			e.storeSessionHistory(req.SessionKey, messages)
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
			delivered, dropped := compress.CapBytes(result.Text, compress.DefaultByteCap, result.Compressed)
			e.emit(newToolResultEvent(turn, tc.ID, tc.Name, result.Text, dropped))
			messages = append(messages, provider.Message{
				Role:       provider.RoleTool,
				ToolCallID: tc.ID,
				Content:    delivered,
			})
		}
		stopContent += content
		stopReasoning += reasoning
	}
}

// toolSchema returns the canonical strict-shaped Parameters map for the named tool from the request-head tool manifest, or nil if the tool is unknown/not validated.
func toolSchema(tools []provider.Tool, name string) map[string]any {
	for _, t := range tools {
		if t.Function.Name == name {
			return t.Function.Parameters
		}
	}
	return nil
}

// execToolCall runs one tool call through the hardened dispatch path: it parses and validates the arguments against the tool's strict schema, then executes only when valid.
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
	start := 0
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem && messages[0].Content == SystemPromptContent() {
		start = 1
	}
	persisted := append([]provider.Message(nil), messages[start:]...)
	e.histMu.Lock()
	defer e.histMu.Unlock()
	e.histories[sessionKey] = persisted
}
