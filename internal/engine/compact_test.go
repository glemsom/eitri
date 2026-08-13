package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// compactCfg returns a compaction config with a small context window and a
// tiny tail token budget so tests cross the 80% threshold and evict the oldest
// body with minimal fixtures (the soft 8k budget would otherwise swallow a
// toy-sized conversation).
func compactCfg() *CompactionConfig {
	return &CompactionConfig{
		Fraction:         DefaultCompactionFraction,
		ContextWindow:    1000,
		TailTurns:        DefaultTailTurns,
		KeepRecentTokens: 4,
		SummaryMaxTokens: DefaultSummaryMaxTokens,
	}
}

// TestEvictPruneRingFenceProtectsSkillContent verifies the optional prune
// ring-fence (ADR-0003 decision 5): a message belonging to a skill activation
// is kept, not evicted, even when the soft budget would drop it.
func TestEvictPruneRingFenceProtectsSkillContent(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleUser, Content: "run the skill"},
		{Role: provider.RoleAssistant, ReasoningContent: "skill leg",
			ToolCalls: []provider.ToolCall{{ID: "s1", Name: "skill", Arguments: `{"name":"go"}`}}},
		{Role: provider.RoleTool, ToolCallID: "s1", Content: "SKILL activated <go-guidelines>..."},
		{Role: provider.RoleUser, Content: "latest prompt"},
	}
	cfg := compactCfg()
	cfg.Prune = true
	body, tail := evict(cfg, msgs)

	if len(body) == 0 {
		t.Fatal("untrimmed prune config produced no evicted body")
	}
	// The skill leg and its tool result must survive in the tail.
	var sawSkill, sawTool bool
	for _, m := range tail {
		if isSkillMessage(m) {
			sawSkill = true
		}
		if m.Role == provider.RoleTool && m.ToolCallID == "s1" {
			sawTool = true
		}
	}
	if !sawSkill || !sawTool {
		t.Fatalf("prune ring-fence evicted skill content: skill=%v tool=%v", sawSkill, sawTool)
	}
}

// compactHandler drives a scripted provider through a compaction crossing:
// turn 1 makes a tool call (history grows), turn 2 makes a tool call and
// reports usage crossing the 80% threshold, the engine issues a non-tool
// summary call, then turn 3 is the final answer. It records every request and
// the assistant reasoning the engine re-emits, so the test can assert the head
// is swapped for the summary and the verbatim tail (reasoning included)
// survives.
type compactHandler struct {
	requests   []provider.Request
	transcript []string
}

func (c *compactHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	c.requests = append(c.requests, req)

	// A summary-generation call: non-tool, no tool_calls, no tool choice.
	if len(req.Tools) == 0 {
		// Capture a marker for reuse as the re-injected summary head.
		for _, m := range req.Messages {
			if m.Role == provider.RoleUser {
				c.transcript = append(c.transcript, "SUMMARY:"+m.Content)
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "## Objective\nKeep working towards the goal.\n## Next Move\nContinue."},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	}

	var toolResults int
	for _, m := range req.Messages {
		if m.Role == provider.RoleTool {
			toolResults++
		}
	}
	switch {
	case toolResults == 0:
		// Turn 1: call a tool so the message history grows.
		c.transcript = append(c.transcript, "T1")
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "turn-one reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_t1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true},
		), nil
	case toolResults == 1:
		// Turn 2: another tool call, usage now crosses the 80% threshold.
		c.transcript = append(c.transcript, "T2")
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "turn-two reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_t2", Type: "function", Name: "bash", Arguments: `{"command":"pwd"}`},
			}, Done: true, Usage: &provider.Usage{
				PromptTokens:     900, // 90% of the 1000-token window
				CompletionTokens: 5,
			}},
		), nil
	default:
		// Final non-tool turn after compaction.
		c.transcript = append(c.transcript, "FINAL")
		return provider.StreamFunc(
			provider.Chunk{Content: "done", FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	}
}

// budgetScripted wraps a Scripted handler so it also declares (via the
// generation-control capability surface) that it honors the Generation Budget
// control — the wire-emitting budget a supporting provider advertises. The
// engine opts the compaction summary turn into that budget, so the summary
// request must carry max_completion_tokens (issue #60).
type budgetScripted struct {
	provider.Scripted
}

// SupportedGenerationControls implements provider.GenerationControlProvider.
func (b *budgetScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlGenerationBudget}, nil
}

// TestCompactionSummaryHonorsGenerationBudget verifies the compaction summary
// special turn — an internal, non-tool generation — opts into a hard Generation
// Budget on a supporting provider: the summary request carries
// max_completion_tokens capped at SummaryMaxTokens, while ordinary agent/tool
// turns in the same run carry no budget (issue #60).
func TestCompactionSummaryHonorsGenerationBudget(t *testing.T) {
	h := &compactHandler{}
	e := New(&budgetScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "go",
		SessionKey: "sess-budget",
	}, AgentOptions{
		Tools:       strictToolDefs(),
		ToolChoice:  "auto",
		Executor:    &mockToolRecorder{},
		MaxTurns:    10,
		Compaction:  compactCfg(),
		OnCompacted: func() {},
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(h.requests) != 4 {
		t.Fatalf("provider requests = %d, want 4 (t1, t2, summary, final)", len(h.requests))
	}
	summary := h.requests[2]
	if len(summary.Tools) != 0 {
		t.Fatalf("summary request carried tools, want a non-tool special turn")
	}
	if summary.MaxOutputTokens != DefaultSummaryMaxTokens {
		t.Fatalf("summary MaxOutputTokens = %d, want %d (SummaryMaxTokens)", summary.MaxOutputTokens, DefaultSummaryMaxTokens)
	}
	// Ordinary agent/tool turns must not carry a generation budget.
	for i, r := range h.requests {
		if i == 2 {
			continue
		}
		if r.MaxOutputTokens != 0 {
			t.Errorf("ordinary turn %d carried MaxOutputTokens=%d, want 0 (no budget)", i, r.MaxOutputTokens)
		}
	}
}

// TestCompactionSkipsSummaryWhenBudgetUnsupported verifies the generation-control
// contract (docs/spec.md §13 / issue #60): a special turn that requires the
// Generation Budget on a provider that cannot honor it fails negotiation, and the
// summary is skipped via the fail-safe path rather than silently running without
// the hard cap. Compaction still happens (eviction frees context) and the run
// completes.
func TestCompactionSkipsSummaryWhenBudgetUnsupported(t *testing.T) {
	// NewScripted has no generation-control capability surface: it honors no
	// controls, so a required Generation Budget fails the contract.
	h := &compactHandler{}
	e := New(provider.NewScripted(h.stream), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "go",
		SessionKey: "sess-nobudget",
	}, AgentOptions{
		Tools:       strictToolDefs(),
		ToolChoice:  "auto",
		Executor:    &mockToolRecorder{},
		MaxTurns:    10,
		Compaction:  compactCfg(),
		OnCompacted: func() {},
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil (compaction still completes on fail-safe skip)", err)
	}
	// T1 (tool) -> T2 (tool + threshold) -> no summary -> FINAL, straight through.
	if len(h.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (t1, t2, final) — summary skipped for unsupported required budget", len(h.requests))
	}
	// No request may carry a budget the provider cannot honor.
	for i, r := range h.requests {
		if r.MaxOutputTokens != 0 {
			t.Errorf("request %d carried MaxOutputTokens=%d on an unsupported provider", i, r.MaxOutputTokens)
		}
	}
}

// TestRunAgentCompactsAtThreshold exercises the proactive 80%-threshold trigger
// through the engine seam (ADR-0003 decision 1/3/4): after a turn reports
// usage crossing the threshold, the engine evicts the oldest body, re-injects
// an anchored summary at the head of the next request, and preserves the
// verbatim tail (including reasoning_content).
func TestRunAgentCompactsAtThreshold(t *testing.T) {
	h := &compactHandler{}
	var compacted bool
	e := New(&budgetScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "go",
		SessionKey: "sess-compact",
	}, AgentOptions{
		Tools:       strictToolDefs(),
		ToolChoice:  "auto",
		Executor:    &mockToolRecorder{},
		MaxTurns:    10,
		Compaction:  compactCfg(),
		OnCompacted: func() { compacted = true },
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if !compacted {
		t.Fatal("expected OnCompacted to fire when the session crossed the 80% threshold")
	}

	// Turn sequence: T1 (tool) -> T2 (tool + high usage) -> summary call -> FINAL.
	if len(h.requests) != 4 {
		t.Fatalf("provider requests = %d, want 4 (t1, t2, summary, final)", len(h.requests))
	}
	summaryReq := h.requests[2]
	if len(summaryReq.Tools) != 0 {
		t.Fatalf("summary generation request carried tools, want a non-tool call")
	}

	// The final request must carry the byte-stable base system prompt at [0]
	// (spec §34 / issue #102), the anchored summary immediately after it (spec
	// §135 / issue #103), and the verbatim tail (the last two tool legs,
	// reasoning included) below.
	final := h.requests[3]
	if len(final.Messages) < 4 {
		t.Fatalf("final request has %d messages, want >= 4 (base + summary head + tail floor)", len(final.Messages))
	}
	base := final.Messages[0]
	if base.Role != provider.RoleSystem || base.Content != SystemPromptContent() {
		t.Errorf("final request base = role %q, want the embedded system prompt at [0]", base.Role)
	}
	// The summary is re-anchored BELOW the immutable base prompt, never before it.
	summary := final.Messages[1]
	if summary.Role != provider.RoleSystem || !strings.Contains(summary.Content, "Objective") {
		t.Errorf("final request summary = role %q content %q, want a system summary anchored on Objective immediately after the base prompt", summary.Role, summary.Content)
	}

	// The verbatim tail must survive byte-for-byte, including reasoning on the
	// assistant turn. Compare against the last two tool legs emitted pre-compaction.
	tailAssistants := 0
	for _, m := range final.Messages {
		if m.Role == provider.RoleAssistant {
			tailAssistants++
			if m.ReasoningContent == "" {
				t.Errorf("tail assistant message lost reasoning: %q", m.Content)
			}
		}
	}
	if tailAssistants < 2 {
		t.Errorf("final request kept %d assistant legs, want the tail floor of >= 2 with reasoning", tailAssistants)
	}

	// Compaction keeps the session cache key (hard cache break, same key).
	for _, r := range h.requests {
		if !r.SetCacheKey || r.SessionKey != "sess-compact" {
			t.Errorf("request lost session cache key: SetCacheKey=%v SessionKey=%q", r.SetCacheKey, r.SessionKey)
		}
	}
}

// overflowHandler fires the emergency trigger: a tool turn grows history, the
// next request reports a context-overflow 400 below the threshold, the engine
// compact-must rebuild the summary head and retry, and the retried turn
// succeeds.
type overflowHandler struct {
	requests int
}

func (h *overflowHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	switch h.requests {
	case 0:
		// First turn: make a tool call so the message history grows and the
		// loop has something to compact.
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "overflow turn one reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_o1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 1}},
		), nil
	case 1:
		// Second tool turn: enough history now that eviction has a body.
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "overflow turn two reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_o2", Type: "function", Name: "bash", Arguments: `{"command":"pwd"}`},
			}, Done: true, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 1}},
		), nil
	case 2:
		// The next request overflows the context window below the proactive
		// threshold (ADR-0003 decision 2).
		h.requests++
		return nil, provider.ErrContextOverflow
	case 3:
		// The compaction summary call (non-tool): provide the anchored summary.
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{Content: "## Objective\nRecovered.\n## Next Move\nRetry."},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	default:
		// The overflowed turn retried after compaction: succeeds.
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{Content: "recovered answer", FinishReason: "stop", Done: true},
		), nil
	}
}

// TestRunAgentOverflowTrigger fires the emergency 400/context-overflow trigger
// below the threshold (ADR-0003 decision 2): the engine compacts and retries
// through the same unified path rather than failing the run with the overflow
// error.
func TestRunAgentOverflowTrigger(t *testing.T) {
	h := &overflowHandler{}
	e := New(&budgetScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{
		Model: "deepseek-v4-flash", Prompt: "go",
	}, AgentOptions{
		Tools:      strictToolDefs(),
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   5,
		Compaction: compactCfg(),
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil (overflow should trigger compaction, not fail)", err)
	}
	if h.requests < 5 {
		t.Fatalf("overflow went through %d provider requests, want >= 5 (2 tool turns, overflow, summary, retry)", h.requests)
	}
}
