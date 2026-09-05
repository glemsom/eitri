package engine

import (
	"context"
	"strings"

	"github.com/glemsom/eitri/internal/constants"
	"github.com/glemsom/eitri/internal/provider"
)

// CompactionConfig configures the unified session compaction engine.
type CompactionConfig struct {
	Fraction         float64
	ContextWindow    int
	TailTurns        int
	KeepRecentTokens int
	SummaryMaxTokens int
	Prune            bool
}

// defaults fills zero fields with the defaults.
func (c *CompactionConfig) defaults() {
	if c.Fraction <= 0 {
		c.Fraction = constants.DefaultCompactionFraction
	}
	if c.ContextWindow <= 0 {
		c.ContextWindow = constants.DefaultContextWindow
	}
	if c.TailTurns <= 0 {
		c.TailTurns = constants.DefaultTailTurns
	}
	if c.KeepRecentTokens <= 0 {
		c.KeepRecentTokens = constants.DefaultKeepRecentTokens
	}
	if c.SummaryMaxTokens <= 0 {
		c.SummaryMaxTokens = constants.DefaultSummaryMaxTokens
	}
}

// shouldCompact reports whether the proactive threshold was crossed: the turn's prompt usage has reached fraction × context window.
func shouldCompact(cfg *CompactionConfig, usage *provider.Usage) bool {
	if usage == nil || cfg.ContextWindow <= 0 {
		return false
	}
	if cfg.Fraction <= 0 {
		cfg.Fraction = constants.DefaultCompactionFraction
	}
	return usage.PromptTokens >= int(float64(cfg.ContextWindow)*cfg.Fraction)
}

func (e *Engine) maybeCompact(ctx context.Context, req RunRequest, opts AgentOptions, messages []provider.Message, force bool, turn int) ([]provider.Message, bool) {
	cfg := opts.Compaction
	if cfg == nil {
		return messages, false
	}
	cfg.defaults()

	if !force && !shouldCompact(cfg, opts.lastUsage) {
		return messages, false
	}

	partition := partitionMessages(messages)
	stableHead := append([]provider.Message(nil), partition.StableHead...)
	messages = partition.PersistedHistory()
	for len(messages) > 0 && partition.IsTransient(messages[0]) {
		stableHead = append(stableHead, messages[0])
		messages = messages[1:]
	}

	body, tail := evict(cfg, messages)
	if len(tail) == 0 || len(tail) == len(messages) {
		if stableHead != nil {
			return append(stableHead, messages...), false
		}
		return messages, false
	}

	newPrefix := append(append([]provider.Message(nil), stableHead...), tail...)
	if len(body) > 0 {
		summary := e.generateSummary(ctx, req, cfg, body)
		if summary != "" {
			summaryHead := append(append([]provider.Message(nil), stableHead...),
				provider.Message{Role: provider.RoleSystem, Content: summary})
			newPrefix = append(summaryHead, tail...)
		}
	}

	if opts.OnCompacted != nil {
		opts.OnCompacted()
	}
	e.emit(CompactedEvent{Turn: turn})
	return newPrefix, true
}

// evict splits messages into the evicted oldest body and the verbatim tail, applying the hard TailTurns floor and the soft KeepRecentTokens budget.
func evict(cfg *CompactionConfig, messages []provider.Message) (body, tail []provider.Message) {
	if len(messages) == 0 {
		return nil, messages
	}
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
	if cfg.Prune {
		partition := partitionMessages(messages)
		for i := range messages {
			if partition.IsTransient(messages[i]) {
				if i < keepStart {
					keepStart = i
				}
				break
			}
		}
	}
	return messages[:keepStart], messages[keepStart:]
}

// generateSummary produces the anchored `## Objective` / `## Next Move` summary of the evicted body via a separate non-tool provider call.
func (e *Engine) generateSummary(ctx context.Context, req RunRequest, cfg *CompactionConfig, body []provider.Message) string {
	bodyText := renderBody(body)
	if estimateString(bodyText) > cfg.ContextWindow/2 {
		return ""
	}

	instruction := "You are compressing an agent conversation for seamless continuation. " +
		"Read the conversation log below and output ONLY the condensed state, keeping the exact headings:" +
		" `## Objective` followed by the current objective, then `## Next Move` followed by the single next action."

	s, err := e.provider.Stream(ctx, provider.Request{
		Model: req.Model,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: instruction},
			{Role: provider.RoleUser, Content: bodyText},
		},
		ThinkingEnabled: false, // a summary needs no chain-of-thought
		SessionKey:      req.SessionKey,
		SetCacheKey:     req.SessionKey != "",
		MaxOutputTokens: cfg.SummaryMaxTokens,
		ProviderID:      req.ProviderID,
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

// isSkillIndexMessage reports whether a message is the injected model-visible
// skill index system message (see RunRequest.SkillIndex). It matches a system
// message carrying the rendered <available_skills> block so history stripping
// can drop it.
func isSkillIndexMessage(m provider.Message) bool {
	return m.Role == provider.RoleSystem && strings.Contains(m.Content, "<available_skills>")
}

// isWorkspaceMessage reports whether a message is the injected per-run
// working-directory system message (see RunRequest.Workspace /
// workspaceDirective). It matches the directive's heading so history stripping
// and compaction can drop or preserve it independently of the byte-stable
// system prompt.
func isWorkspaceMessage(m provider.Message) bool {
	return m.Role == provider.RoleSystem && strings.Contains(m.Content, "## Working directory")
}

// isRepoInstructionMessage reports whether a message is the injected
// workspace-root AGENTS.md system message (see RunRequest.RepoInstructions /
// repoInstructionsDirective). It matches the directive's heading so history
// stripping and compaction can drop or preserve it independently of the
// byte-stable system prompt.
func isRepoInstructionMessage(m provider.Message) bool {
	return m.Role == provider.RoleSystem && strings.Contains(m.Content, "## Repository instructions (AGENTS.md)")
}

// renderBody serializes the evicted body messages into a flat transcript the summary model can consume.
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

// estimateTokens is a deterministic token approximation (chars/4) used for compaction budgeting, covering the tail-budget content that matters for the eviction decision: assistant answer text and reasoning_content.
func estimateTokens(m provider.Message) int {
	return estimateString(m.Content) + estimateString(m.ReasoningContent)
}

// estimateString approximates token count of text as chars/4 (stable, deterministic, and sufficient for the compaction budget).
func estimateString(text string) int {
	return (len(text) + 3) / 4
}

// capTokens truncates text to the first n tokens estimated via estimateTokens' char/4 heuristic, without cutting mid-run bytes.
func capTokens(text string, n int) string {
	max := n * 4
	if len(text) <= max {
		return text
	}
	return text[:max]
}

func ResolveCompaction(ctx context.Context, p provider.Provider, enabled bool) (*CompactionConfig, error) {
	if !enabled {
		return nil, nil
	}
	if _, err := provider.NegotiateGenerationControls(ctx, p, []provider.ControlRequirement{{
		Control:  provider.GenerationControlGenerationBudget,
		Required: true,
	}}); err != nil {
		return nil, err
	}
	return &CompactionConfig{}, nil
}

func (e *Engine) ResolveCompaction(ctx context.Context, enabled bool) (*CompactionConfig, error) {
	return ResolveCompaction(ctx, e.provider, enabled)
}
