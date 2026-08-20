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

func TestMaybeCompactKeepsSkillInjectSystemMessage(t *testing.T) {
	t.Parallel()
	// A fail-safe provider: summary generation returns nothing, so maybeCompact
	// takes its head+tail path and must still preserve the injected skill head.
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Done: true}), nil
	}), &mockTranscript{})

	skill := "<skill_content name=\"improve-codebase-architecture\">\nDo the architecture thing.\n</skill_content>\n"
	// A long run whose message list opens [system(Eitri), system(<skill_content>), user, ...]:
	// the two assistant legs force the tail floor past the skill head, so without the
	// stable-head fix the skill system message is evicted into the body and lost.
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleSystem, Content: skill},
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer one"},
		{Role: provider.RoleTool, ToolCallID: "t1", Content: "result"},
		{Role: provider.RoleUser, Content: "mid prompt"},
		{Role: provider.RoleAssistant, Content: "old answer two"},
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
		if m.Role == provider.RoleSystem && strings.Contains(m.Content, "skill_content") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("injected skill system message dropped by compaction:\n%s", got)
	}
}

func TestIsSkillMessageRecognizesSkillContentSystemMessage(t *testing.T) {
	t.Parallel()
	if !isSkillMessage(provider.Message{Role: provider.RoleSystem,
		Content: "<skill_content name=\"go\">follow the guidelines</skill_content>"}) {
		t.Fatal("isSkillMessage must recognize the injected <skill_content> system message")
	}
	// A model-invoked skill tool call and its SKILL-carrying tool result stay recognized.
	if !isSkillMessage(provider.Message{Role: provider.RoleAssistant,
		ToolCalls: []provider.ToolCall{{Name: "skill"}}}) {
		t.Fatal("isSkillMessage must keep recognizing the assistant skill tool call")
	}
	if !isSkillMessage(provider.Message{Role: provider.RoleTool, Content: "SKILL activated"}) {
		t.Fatal("isSkillMessage must keep recognizing a SKILL tool result")
	}
}

func TestEvictPruneRingFenceProtectsSkillContent(t *testing.T) {
	t.Parallel()
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
	if summary.MaxOutputTokens != constants.DefaultSummaryMaxTokens {
		t.Fatalf("summary MaxOutputTokens = %d, want %d (SummaryMaxTokens)", summary.MaxOutputTokens, constants.DefaultSummaryMaxTokens)
	}
	for i, r := range h.requests {
		if i == 2 {
			continue
		}
		if r.MaxOutputTokens != 0 {
			t.Errorf("ordinary turn %d carried MaxOutputTokens=%d, want 0 (no budget)", i, r.MaxOutputTokens)
		}
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

func TestRunAgentCompactsAtThreshold(t *testing.T) {
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

	if !compacted {
		t.Fatal("expected OnCompacted to fire when the session crossed the 80% threshold")
	}

	if len(h.requests) != 4 {
		t.Fatalf("provider requests = %d, want 4 (t1, t2, summary, final)", len(h.requests))
	}
	summaryReq := h.requests[2]
	if len(summaryReq.Tools) != 0 {
		t.Fatalf("summary generation request carried tools, want a non-tool call")
	}

	final := h.requests[3]
	if len(final.Messages) < 4 {
		t.Fatalf("final request has %d messages, want >= 4 (base + summary head + tail floor)", len(final.Messages))
	}
	base := final.Messages[0]
	if base.Role != provider.RoleSystem || base.Content != SystemPromptContent() {
		t.Errorf("final request base = role %q, want the embedded system prompt at [0]", base.Role)
	}
	summary := final.Messages[1]
	if summary.Role != provider.RoleSystem || !strings.Contains(summary.Content, "Objective") {
		t.Errorf("final request summary = role %q content %q, want a system summary anchored on Objective immediately after the base prompt", summary.Role, summary.Content)
	}

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

	for i, r := range h.requests {
		if len(r.Tools) > 0 && !reflect.DeepEqual(r.Tools, strictToolDefs()) {
			t.Errorf("request %d tools drifted from the canonical manifest (cache-prefix break): %v", i, r.Tools)
		}
	}

	for _, r := range h.requests {
		if !r.SetCacheKey || r.SessionKey != "sess-compact" {
			t.Errorf("request lost session cache key: SetCacheKey=%v SessionKey=%q", r.SetCacheKey, r.SessionKey)
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
