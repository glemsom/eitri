package engine

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/provider"
)

// Session compaction constants (ADR-0003). The engine compacts
// proactively at a configurable fraction of the context window and
// emergently on a provider context-overflow, keeping a verbatim tail and
// folding the evicted body into an anchored summary re-injected at the head.
const (
	// DefaultCompactionFraction is the default context-utilization trigger.
	DefaultCompactionFraction = 0.8
	// DefaultContextWindow is deepseek-v4-flash's ~1M-token context (ADR-0003).
	DefaultContextWindow = 1 << 20
	// DefaultTailTurns is the hard floor of assistant+user pairs preserved
	// verbatim (never evicted even if over the soft budget).
	DefaultTailTurns = 2
	// DefaultKeepRecentTokens is the soft token budget for the verbatim tail,
	// reasoning_content included (ADR-0003 decision 3).
	DefaultKeepRecentTokens = 8000
	// DefaultSummaryMaxTokens caps the anchored LLM summary (ADR-0003 decision 4).
	DefaultSummaryMaxTokens = 4096
)

// CompactionConfig configures the unified session compaction engine. Zero
// values fall back to the ADR-0003 defaults. Prune, when true, ring-fences
// ["skill"]-tagged content from eviction and never truncates silently
// (ADR-0003 decision 5).
type CompactionConfig struct {
	Fraction         float64
	ContextWindow    int
	TailTurns        int
	KeepRecentTokens int
	SummaryMaxTokens int
	Prune            bool
}

// defaults fills zero fields with the ADR-0003 defaults.
func (c *CompactionConfig) defaults() {
	if c.Fraction <= 0 {
		c.Fraction = DefaultCompactionFraction
	}
	if c.ContextWindow <= 0 {
		c.ContextWindow = DefaultContextWindow
	}
	if c.TailTurns <= 0 {
		c.TailTurns = DefaultTailTurns
	}
	if c.KeepRecentTokens <= 0 {
		c.KeepRecentTokens = DefaultKeepRecentTokens
	}
	if c.SummaryMaxTokens <= 0 {
		c.SummaryMaxTokens = DefaultSummaryMaxTokens
	}
}

// shouldCompact reports whether the proactive threshold was crossed: the turn's
// prompt usage has reached fraction × context window (ADR-0003 decision 1).
func shouldCompact(cfg *CompactionConfig, usage *provider.Usage) bool {
	if usage == nil || cfg.ContextWindow <= 0 {
		return false
	}
	if cfg.Fraction <= 0 {
		cfg.Fraction = DefaultCompactionFraction
	}
	return usage.PromptTokens >= int(float64(cfg.ContextWindow)*cfg.Fraction)
}

// maybeCompact runs the unified compaction engine on messages when the
// proactive threshold has been crossed (or force is set for the emergency
// overflow path). It returns the (possibly compacted) message list and whether
// a compaction actually happened. It never fails the run: a summary-generation
// failure is a fail-safe skip (ADR-0003 decision 4) that still frees context by
// dropping the oldest body.
func (e *Engine) maybeCompact(ctx context.Context, req RunRequest, opts AgentOptions, messages []provider.Message, force bool, turn int) ([]provider.Message, bool) {
	cfg := opts.Compaction
	if cfg == nil {
		return messages, false
	}
	cfg.defaults()

	if !force && !shouldCompact(cfg, opts.lastUsage) {
		return messages, false
	}

	// Stable-head awareness (spec §34 / issue #102): the embedded base system
	// prompt is the immutable request head, anchored at [0] on every run path.
	// Pull it out before eviction so the body-folding and summary-anchoring
	// never consume or displace it; it is reattached first in the rebuilt head.
	stableHead := []provider.Message(nil)
	if len(messages) > 0 && messages[0].Role == provider.RoleSystem && messages[0].Content == SystemPromptContent() {
		stableHead = messages[:1]
		messages = messages[1:]
	}

	body, tail := evict(cfg, messages)
	if len(tail) == 0 || len(tail) == len(messages) {
		// Nothing evictable; reattach the (unchanged) stable head and bail.
		if stableHead != nil {
			return append(stableHead, messages...), false
		}
		return messages, false
	}

	newPrefix := append(append([]provider.Message(nil), stableHead...), tail...)
	if len(body) > 0 {
		summary := e.generateSummary(ctx, req, cfg, body)
		if summary != "" {
			// Re-anchor the compacted summary BELOW the immutable stable head:
			// the base prompt stays at [0], the Objective/Next-Move summary
			// follows it, then the verbatim tail (spec §135 / issue #103).
			summaryHead := append(append([]provider.Message(nil), stableHead...),
				provider.Message{Role: provider.RoleSystem, Content: summary})
			newPrefix = append(summaryHead, tail...)
		}
	}

	if opts.OnCompacted != nil {
		opts.OnCompacted()
	}
	// Surface the compaction marker on the observer seam (issue #81): the TUI
	// renders a read-only [compacted] status entry, never blocking the run.
	e.emit(CompactedEvent{Turn: turn})
	return newPrefix, true
}

// evict splits messages into the evicted oldest body and the verbatim tail,
// applying the hard TailTurns floor and the soft KeepRecentTokens budget
// (ADR-0003 decision 3,4). The tail always preserves at least TailTurns
// assistant+user pairs and extends backward while within the soft token budget.
func evict(cfg *CompactionConfig, messages []provider.Message) (body, tail []provider.Message) {
	if len(messages) == 0 {
		return nil, messages
	}
	// Locate the hard floor: the start of the last TailTurns assistant legs.
	hardStart := 0
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == provider.RoleAssistant {
			seen++
			if seen == cfg.TailTurns {
				hardStart = i
				break
			}
		}
	}
	// Walk further back from the hard floor, keeping older tail messages only
	// while the cumulative token estimate stays within the soft budget.
	keepStart := hardStart
	budget := cfg.KeepRecentTokens
	for i := hardStart; i < len(messages); i++ {
		budget -= estimateTokens(messages[i])
	}
	for i := hardStart - 1; i >= 0 && budget > 0; i-- {
		tok := estimateTokens(messages[i])
		if tok > budget {
			break
		}
		budget -= tok
		keepStart = i
	}
	// Skill-content ring-fence: when Prune is on, never evict a message that is
	// part of a skill activation, even if the soft budget would drop it
	// (ADR-0003 decision 5).
	if cfg.Prune {
		for i := 0; i < len(messages); i++ {
			if isSkillMessage(messages[i]) {
				if i < keepStart {
					keepStart = i
				}
				break
			}
		}
	}
	return messages[:keepStart], messages[keepStart:]
}

// generateSummary produces the anchored `## Objective` / `## Next Move` summary
// of the evicted body via a separate non-tool provider call (ADR-0003 decision
// 4). It returns "" for a fail-safe skip: a provider error, or a body too large
// to leave room for the summary round-trip. The summary is capped at
// SummaryMaxTokens.
func (e *Engine) generateSummary(ctx context.Context, req RunRequest, cfg *CompactionConfig, body []provider.Message) string {
	bodyText := renderBody(body)
	// Fail-safe skip: if the body alone would exhaust the reserve for the
	// summary round-trip, skip the summary rather than risk a malformed prefix.
	if estimateString(bodyText) > cfg.ContextWindow/2 {
		return ""
	}

	instruction := "You are compressing an agent conversation for seamless continuation. " +
		"Read the conversation log below and output ONLY the condensed state, keeping the exact headings:" +
		" `## Objective` followed by the current objective, then `## Next Move` followed by the single next action."

	// The summary generation is a special turn that opts into a hard Generation
	// Budget (issue #60): it requests generation_budget as
	// required, so negotiation either honors it (a required control is always
	// honored when it is supported) or fails fast before any wire call. The
	// request carries a hard max_completion_tokens cap on a supporting provider;
	// a provider that cannot honor the budget is skipped by the existing fail-safe
	// path (the eviction still frees context, and the local SummaryMaxTokens cap
	// remains the safety floor, ADR-0003 decision 4).
	if _, err := e.NegotiateGenerationControls(ctx, []provider.ControlRequirement{
		{Control: provider.GenerationControlGenerationBudget, Required: true},
	}); err != nil {
		return "" // fail-safe skip: required Generation Budget unavailable
	}

	s, err := e.provider.Stream(ctx, provider.Request{
		Model: req.Model,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: instruction},
			{Role: provider.RoleUser, Content: bodyText},
		},
		ThinkingEnabled: false, // a summary needs no chain-of-thought
		SessionKey:      req.SessionKey,
		SetCacheKey:     req.SessionKey != "",
		// The hard wire-backed output cap mirrors SummaryMaxTokens (ADR-0003
		// decision 4); the local capTokens floor remains the safety net.
		MaxOutputTokens: cfg.SummaryMaxTokens,
	})
	if err != nil {
		return "" // fail-safe skip
	}

	var out strings.Builder
	for {
		c, cerr := s.Next()
		if cerr != nil {
			break // stream ended or errored: no summary, fail-safe skip
		}
		out.WriteString(c.Content)
		if c.Done {
			break
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return ""
	}
	if estimateString(text) > cfg.SummaryMaxTokens {
		text = capTokens(text, cfg.SummaryMaxTokens)
	}
	return text
}

// isSkillMessage reports whether a message belongs to a skill activation and so
// is ring-fenced from eviction when Prune is enabled (ADR-0003
// decision 5). An assistant message that carries a tool call naming the built-in
// "skill" tool, or the matching tool result, is protected.
func isSkillMessage(m provider.Message) bool {
	if m.Role == provider.RoleAssistant {
		for _, tc := range m.ToolCalls {
			if tc.Name == "skill" {
				return true
			}
		}
	}
	if m.Role == provider.RoleTool && m.Content != "" && strings.Contains(m.Content, "SKILL") {
		// Best-effort marker check: skill tool results carry a SKILL header.
		return true
	}
	return false
}

// renderBody serializes the evicted body messages into a flat transcript the
// summary model can consume.
func renderBody(messages []provider.Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(strings.ToUpper(string(m.Role)))
		b.WriteString(": ")
		b.WriteString(m.Content)
		if m.ReasoningContent != "" {
			b.WriteString(" [thinking: ")
			b.WriteString(m.ReasoningContent)
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// estimateTokens is a deterministic token approximation (chars/4) used for
// compaction budgeting, covering the tail-budget content that matters for the
// eviction decision: assistant answer text and reasoning_content (ADR-0003
// decision 3). It is stable across runs so the engine is testable
// deterministically at the seam.
func estimateTokens(m provider.Message) int {
	return estimateString(m.Content) + estimateString(m.ReasoningContent)
}

// estimateString approximates token count of text as chars/4 (stable,
// deterministic, and sufficient for the compaction budget).
func estimateString(text string) int {
	return (len(text) + 3) / 4
}

// capTokens truncates text to the first n tokens estimated via estimateTokens'
// char/4 heuristic, without cutting mid-run bytes. It is a fail-safe so a
// runaway summary never grows past the cap.
func capTokens(text string, n int) string {
	max := n * 4
	if len(text) <= max {
		return text
	}
	return text[:max]
}
