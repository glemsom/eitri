package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/constants"
	"github.com/glemsom/eitri/internal/provider"
)

func compactCfg() *CompactionConfig {
	return &CompactionConfig{
		Fraction:         constants.DefaultCompactionFraction,
		ContextWindow:    1000,
		TailTurns:        constants.DefaultTailTurns,
		KeepRecentTokens: 4,
		SummaryMaxTokens: constants.DefaultSummaryMaxTokens,
	}
}

func TestMaybeCompactKeepsSkillInjectInUserLayer(t *testing.T) {
	t.Parallel()
	// A fail-safe provider: summary generation returns nothing, so maybeCompact
	// takes its head+tail path and must still preserve the injected skill head.
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Done: true}), nil
	}), &mockTranscript{})

	skill := "<skill_content name=\"improve-codebase-architecture\">\nDo the architecture thing.\n</skill_content>\n"
	// A long run whose message list opens [system(Eitri), user(<skill_content>+prompt), ...]:
	// the two assistant legs force the tail floor past the skill head, so without the
	// stable-head fix the skill user message is evicted into the body and lost.
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleUser, Content: skill + "\n\nUser request:\nold prompt"},
		{Role: provider.RoleAssistant, Content: "old answer one"},
		{Role: provider.RoleTool, ToolCallID: "t1", Content: "result"},
		{Role: provider.RoleUser, Content: "mid prompt"},
		{Role: provider.RoleAssistant, Content: "old answer two"},
		{Role: provider.RoleUser, Content: "later prompt"},
		{Role: provider.RoleAssistant, Content: "old answer three"},
		{Role: provider.RoleUser, Content: "latest prompt"},
	}

	cfg := compactCfg()
	cfg.Prune = true
	got, ok := e.maybeCompact(context.Background(), RunRequest{}, AgentOptions{
		Compaction: cfg,
		lastUsage:  &provider.Usage{PromptTokens: 999},
	}, messages, true, 1)
	if !ok {
		t.Fatal("expected compaction to fire on a forced overflow")
	}

	var saw bool
	for _, m := range got {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "skill_content") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("injected skill user message dropped by compaction:\n%v", got)
	}
}

func TestIsSkillMessageRecognizesSkillContentInUserLayer(t *testing.T) {
	t.Parallel()
	// The slash-injected <skill_content> directive in the user layer is what the
	// compact ring-fence protects; the model has no `skill` tool.
	if !(messagePartition{}).IsTransient(provider.Message{Role: provider.RoleUser,
		Content: "<skill_content name=\"go\">follow the guidelines</skill_content>"}) {
		t.Fatal("isSkillMessage must recognize the slash-injected <skill_content> directive in the user layer")
	}
	if (messagePartition{}).IsTransient(provider.Message{Role: provider.RoleAssistant,
		ToolCalls: []provider.ToolCall{{Name: "skill"}}}) {
		t.Fatal("isSkillMessage must not recognize a skill tool call (no such model tool)")
	}
}

func TestEvictPruneRingFenceProtectsSkillContent(t *testing.T) {
	t.Parallel()
	// The slash-injected skill directive sits in the user layer as <skill_content>
	// and must survive pruning even as older evictable content is trimmed.
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleUser, Content: "<skill_content name=\"go\">follow the guidelines</skill_content>\n\napply the go skill"},
		{Role: provider.RoleAssistant, Content: "latest answer"},
	}
	cfg := compactCfg()
	cfg.Prune = true
	body, tail := evict(cfg, msgs)

	if len(body) == 0 {
		t.Fatal("untrimmed prune config produced no evicted body")
	}
	var sawSkill bool
	for _, m := range tail {
		if (messagePartition{}).IsTransient(m) {
			sawSkill = true
		}
	}
	if !sawSkill {
		t.Fatalf("prune ring-fence evicted the injected skill user message; tail=%v", tail)
	}
}

type compactHandler struct {
	requests   []provider.Request
	transcript []string
}

func (c *compactHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	c.requests = append(c.requests, req)

	if len(req.Tools) == 0 {
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
		c.transcript = append(c.transcript, "T1")
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "turn-one reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_t1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true},
		), nil
	case toolResults == 1:
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
		c.transcript = append(c.transcript, "FINAL")
		return provider.StreamFunc(
			provider.Chunk{Content: "done", FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	}
}

type budgetScripted struct {
	provider.Scripted
}

func (b *budgetScripted) SupportedGenerationControls(context.Context) ([]provider.GenerationControl, error) {
	return []provider.GenerationControl{provider.GenerationControlGenerationBudget}, nil
}

func TestCompactionSummaryHonorsGenerationBudget(t *testing.T) {
	t.Parallel()
	h := &compactHandler{}
	e := New(&budgetScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("old answer ", 20)},
		{Role: provider.RoleTool, ToolCallID: "t1", Content: "tool result"},
		{Role: provider.RoleUser, Content: "mid prompt"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("mid answer ", 20)},
		{Role: provider.RoleUser, Content: "latest prompt"},
	}

	_, ok := e.maybeCompact(context.Background(), RunRequest{Model: "deepseek-v4-flash", SessionKey: "sess-budget"}, AgentOptions{
		Compaction: compactCfg(),
	}, messages, true, 1)
	if !ok {
		t.Fatal("forced compaction did not run")
	}
	if len(h.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 summary request", len(h.requests))
	}
	summary := h.requests[0]
	if len(summary.Tools) != 0 {
		t.Fatalf("summary request carried tools, want a non-tool special turn")
	}
	if summary.MaxOutputTokens != constants.DefaultSummaryMaxTokens {
		t.Fatalf("summary MaxOutputTokens = %d, want %d (SummaryMaxTokens)", summary.MaxOutputTokens, constants.DefaultSummaryMaxTokens)
	}
}

func TestCompactionSkipsSummaryWhenBudgetUnsupported(t *testing.T) {
	t.Parallel()
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
	if len(h.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (t1, t2, final) — summary skipped for unsupported required budget", len(h.requests))
	}
	for i, r := range h.requests {
		if r.MaxOutputTokens != 0 {
			t.Errorf("request %d carried MaxOutputTokens=%d on an unsupported provider", i, r.MaxOutputTokens)
		}
	}
}

func TestRunAgentDoesNotCompactAtThreshold(t *testing.T) {
	t.Parallel()
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

	if compacted {
		t.Fatal("OnCompacted fired for high prompt-token usage; proactive compaction should be disabled")
	}

	if len(h.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3 (t1, t2, final with no summary request)", len(h.requests))
	}
	for i, req := range h.requests {
		if len(req.Tools) == 0 {
			t.Fatalf("request %d was a summary-generation request; proactive compaction should be disabled", i)
		}
		if !reflect.DeepEqual(req.Tools, strictToolDefs()) {
			t.Errorf("request %d tools drifted from the canonical manifest (cache-prefix break): %v", i, req.Tools)
		}
		if !req.SetCacheKey || req.SessionKey != "sess-compact" {
			t.Errorf("request lost session cache key: SetCacheKey=%v SessionKey=%q", req.SetCacheKey, req.SessionKey)
		}
	}

	final := h.requests[2]
	for _, msg := range final.Messages {
		if strings.Contains(msg.Content, "## Conversation Summary") {
			t.Fatalf("final request included compaction summary despite no context overflow; messages=%v", final.Messages)
		}
	}
}

type overflowHandler struct {
	requests int
}

func (h *overflowHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	switch h.requests {
	case 0:
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "overflow turn one reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_o1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 1}},
		), nil
	case 1:
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "overflow turn two reasoning"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_o2", Type: "function", Name: "bash", Arguments: `{"command":"pwd"}`},
			}, Done: true, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 1}},
		), nil
	case 2:
		h.requests++
		return nil, provider.ErrContextOverflow
	case 3:
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{Content: "## Objective\nRecovered.\n## Next Move\nRetry."},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	default:
		h.requests++
		return provider.StreamFunc(
			provider.Chunk{Content: "recovered answer", FinishReason: "stop", Done: true},
		), nil
	}
}

func TestRunAgentOverflowTrigger(t *testing.T) {
	t.Parallel()
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

func TestRunAgentOverflowDisabledReturnsClearError(t *testing.T) {
	t.Parallel()
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return nil, provider.ErrContextOverflow
	}), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{})
	if err == nil || err.Error() != "Provider rejected the request because the context is too large. Context overflow recovery is disabled; enable it or start a new session." {
		t.Fatalf("RunAgent error = %v, want disabled-recovery guidance", err)
	}
}

func TestRunAgentOverflowRecoveryOnlyOncePerRun(t *testing.T) {
	t.Parallel()
	h := &overflowHandler{}
	e := New(&budgetScripted{Scripted: *provider.NewScripted(func(ctx context.Context, req provider.Request) (provider.Stream, error) {
		if h.requests >= 4 {
			h.requests++
			return nil, provider.ErrContextOverflow
		}
		return h.stream(ctx, req)
	})}, &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:      strictToolDefs(),
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   5,
		Compaction: compactCfg(),
	})
	if err == nil || err.Error() != "Provider rejected the request because the context is too large. Eitri summarized older history and retried once, but the request is still too large. Start a new session or reduce attached/tool output." {
		t.Fatalf("RunAgent error = %v, want failed-recovery guidance", err)
	}
	if h.requests != 5 {
		t.Fatalf("provider requests = %d, want 5 (2 tool turns, overflow, summary, retry overflow)", h.requests)
	}
}

type unsupportedSummaryProvider struct{ t *testing.T }

func (p unsupportedSummaryProvider) Stream(context.Context, provider.Request) (provider.Stream, error) {
	p.t.Fatal("provider network work started during compaction setup")
	return nil, nil
}

func TestResolveCompactionRejectsProviderWithoutSummaryBudget(t *testing.T) {
	t.Parallel()
	_, err := ResolveCompaction(context.Background(), unsupportedSummaryProvider{t: t}, true)
	if err == nil {
		t.Fatal("ResolveCompaction() error = nil, want unsupported summary capability error")
	}
}

func TestResolveCompactionReturnsExplicitModes(t *testing.T) {
	t.Parallel()
	disabled, err := ResolveCompaction(context.Background(), provider.NewScripted(nil), false)
	if err != nil || disabled != nil {
		t.Fatalf("ResolveCompaction(disabled) = (%v, %v), want (nil, nil)", disabled, err)
	}
	enabled, err := ResolveCompaction(context.Background(), &budgetScripted{}, true)
	if err != nil || enabled == nil {
		t.Fatalf("ResolveCompaction(enabled) = (%v, %v), want supported config", enabled, err)
	}
}
